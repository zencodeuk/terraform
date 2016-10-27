package terraform

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// DebugInfo is the global handler for writing the debug archive. All methods
// are safe to call concurrently. Setting DebugInfo to nil will disable writing
// the debug archive. All methods are safe to call on the nil value.
var DebugInfo *debugInfo

// SetDebugInfo sets the debug options for the terraform package. Currently
// this just sets the path where the archive will be written.
func SetDebugInfo(path string) error {
	if os.Getenv("TF_DEBUG") == "" {
		return nil
	}

	di, err := newDebugInfo(path)
	if err != nil {
		return err
	}

	DebugInfo = di
	return nil
}

func newDebugInfo(dir string) (*debugInfo, error) {
	err := os.MkdirAll(dir, 0755)
	if err != nil {
		return nil, err
	}

	// FIXME: not guaranteed unique, but good enough for now
	name := fmt.Sprintf("debug-%s", time.Now().Format("2006-01-02-15-04-05.999999999"))
	archivePath := filepath.Join(dir, name+".tar.gz")

	f, err := os.OpenFile(archivePath, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0666)
	if err != nil {
		return nil, err
	}

	gz := gzip.NewWriter(f)

	d := &debugInfo{
		name:       name,
		file:       f,
		compressor: gz,
		archive:    tar.NewWriter(gz),
	}

	// create the subdirs we need
	topHdr := &tar.Header{Name: name,
		Typeflag: tar.TypeDir,
		Mode:     0755,
	}
	graphsHdr := &tar.Header{
		Name:     name + "/graphs",
		Typeflag: tar.TypeDir,
		Mode:     0755,
	}
	err = d.archive.WriteHeader(topHdr)
	// if the first errors, the second will too
	err = d.archive.WriteHeader(graphsHdr)
	if err != nil {
		f.Close()
		return nil, err
	}

	return d, nil
}

type debugInfo struct {
	sync.Mutex

	// directory name
	name string

	// current operation phase
	phase string

	// step is monotonic counter for for recording the order of operations
	step int

	// flag to protect Close()
	closed bool

	// the debug log output goes here
	file       *os.File
	compressor *gzip.Writer
	archive    *tar.Writer
}

func (d *debugInfo) SetPhase(phase string) {
	if d == nil {
		return
	}
	d.Lock()
	defer d.Unlock()

	d.phase = phase
}

func (d *debugInfo) Close() error {
	if d == nil {
		return nil
	}

	d.Lock()
	defer d.Unlock()

	if d.closed {
		return nil
	}
	d.closed = true

	d.archive.Close()
	d.compressor.Close()
	return d.file.Close()
}

// make sure things are always flushed in the correct order
func (d *debugInfo) flush() {
	d.archive.Flush()
	d.compressor.Flush()
	d.file.Sync()
}

// Write the current graph state to the debug log in dot format.
func (d *debugInfo) WriteGraph(dg *DebugGraph) error {
	if d == nil {
		return nil
	}

	if dg == nil {
		return nil
	}

	d.Lock()
	defer d.Unlock()

	// If we crash, the file won't be correctly closed out, but we can rebuild
	// the archive if we have to as long as every file has been flushed and
	// sync'ed.
	defer d.flush()

	d.writeFile(dg.Name, dg.buf.Bytes())

	dotPath := fmt.Sprintf("%s/graphs/%d-%s-%s.dot", d.name, d.step, d.phase, dg.Name)
	d.step++

	dotBytes := dg.DotBytes()
	hdr := &tar.Header{
		Name: dotPath,
		Mode: 0644,
		Size: int64(len(dotBytes)),
	}

	err := d.archive.WriteHeader(hdr)
	if err != nil {
		return err
	}

	_, err = d.archive.Write(dotBytes)
	return err
}

// WriteFile writes data as a single file to the debug arhive.
func (d *debugInfo) WriteFile(name string, data []byte) error {
	if d == nil {
		return nil
	}

	d.Lock()
	defer d.Unlock()
	return d.writeFile(name, data)
}

func (d *debugInfo) writeFile(name string, data []byte) error {
	defer d.flush()
	path := fmt.Sprintf("%s/%d-%s-%s", d.name, d.step, d.phase, name)
	d.step++

	hdr := &tar.Header{
		Name: path,
		Mode: 0644,
		Size: int64(len(data)),
	}
	err := d.archive.WriteHeader(hdr)
	if err != nil {
		return err
	}

	_, err = d.archive.Write(data)
	return err
}

type DebugHook struct{}

func (*DebugHook) PreApply(ii *InstanceInfo, is *InstanceState, id *InstanceDiff) (HookAction, error) {
	var buf bytes.Buffer

	if ii != nil {
		buf.WriteString(ii.HumanId() + "\n")
	}

	if is != nil {
		buf.WriteString(is.String() + "\n")
	}

	idCopy, err := id.Copy()
	if err != nil {
		return HookActionContinue, err
	}
	js, err := json.MarshalIndent(idCopy, "", "  ")
	if err != nil {
		return HookActionContinue, err
	}
	buf.Write(js)

	DebugInfo.WriteFile("hook-PreApply", buf.Bytes())

	return HookActionContinue, nil
}

func (*DebugHook) PostApply(ii *InstanceInfo, is *InstanceState, err error) (HookAction, error) {
	var buf bytes.Buffer

	if ii != nil {
		buf.WriteString(ii.HumanId() + "\n")
	}

	if is != nil {
		buf.WriteString(is.String() + "\n")
	}

	if err != nil {
		buf.WriteString(err.Error())
	}

	DebugInfo.WriteFile("hook-PostApply", buf.Bytes())

	return HookActionContinue, nil
}

func (*DebugHook) PreDiff(ii *InstanceInfo, is *InstanceState) (HookAction, error) {
	var buf bytes.Buffer
	if ii != nil {
		buf.WriteString(ii.HumanId() + "\n")
	}

	if is != nil {
		buf.WriteString(is.String())
		buf.WriteString("\n")
	}
	DebugInfo.WriteFile("hook-PreDiff", buf.Bytes())

	return HookActionContinue, nil
}

func (*DebugHook) PostDiff(ii *InstanceInfo, id *InstanceDiff) (HookAction, error) {
	var buf bytes.Buffer
	if ii != nil {
		buf.WriteString(ii.HumanId() + "\n")
	}

	idCopy, err := id.Copy()
	if err != nil {
		return HookActionContinue, err
	}
	js, err := json.MarshalIndent(idCopy, "", "  ")
	if err != nil {
		return HookActionContinue, err
	}
	buf.Write(js)

	DebugInfo.WriteFile("hook-PostDiff", buf.Bytes())

	return HookActionContinue, nil
}

func (*DebugHook) PreProvisionResource(ii *InstanceInfo, is *InstanceState) (HookAction, error) {
	var buf bytes.Buffer
	if ii != nil {
		buf.WriteString(ii.HumanId() + "\n")
	}

	if is != nil {
		buf.WriteString(is.String())
		buf.WriteString("\n")
	}
	DebugInfo.WriteFile("hook-PreProvisionResource", buf.Bytes())

	return HookActionContinue, nil
}

func (*DebugHook) PostProvisionResource(ii *InstanceInfo, is *InstanceState) (HookAction, error) {
	var buf bytes.Buffer
	if ii != nil {
		buf.WriteString(ii.HumanId())
		buf.WriteString("\n")
	}

	if is != nil {
		buf.WriteString(is.String())
		buf.WriteString("\n")
	}
	DebugInfo.WriteFile("hook-PostProvisionResource", buf.Bytes())
	return HookActionContinue, nil
}

func (*DebugHook) PreProvision(ii *InstanceInfo, s string) (HookAction, error) {
	var buf bytes.Buffer
	if ii != nil {
		buf.WriteString(ii.HumanId())
		buf.WriteString("\n")
	}
	buf.WriteString(s + "\n")

	DebugInfo.WriteFile("hook-PreProvision", buf.Bytes())
	return HookActionContinue, nil
}

func (*DebugHook) PostProvision(ii *InstanceInfo, s string) (HookAction, error) {
	var buf bytes.Buffer
	if ii != nil {
		buf.WriteString(ii.HumanId() + "\n")
	}
	buf.WriteString(s + "\n")

	DebugInfo.WriteFile("hook-PostProvision", buf.Bytes())
	return HookActionContinue, nil
}

func (*DebugHook) ProvisionOutput(ii *InstanceInfo, s1 string, s2 string) {
	var buf bytes.Buffer
	if ii != nil {
		buf.WriteString(ii.HumanId())
		buf.WriteString("\n")
	}
	buf.WriteString(s1 + "\n")
	buf.WriteString(s2 + "\n")

	DebugInfo.WriteFile("hook-ProvisionOutput", buf.Bytes())
}

func (*DebugHook) PreRefresh(ii *InstanceInfo, is *InstanceState) (HookAction, error) {
	var buf bytes.Buffer
	if ii != nil {
		buf.WriteString(ii.HumanId() + "\n")
	}

	if is != nil {
		buf.WriteString(is.String())
		buf.WriteString("\n")
	}
	DebugInfo.WriteFile("hook-PreRefresh", buf.Bytes())
	return HookActionContinue, nil
}

func (*DebugHook) PostRefresh(ii *InstanceInfo, is *InstanceState) (HookAction, error) {
	var buf bytes.Buffer
	if ii != nil {
		buf.WriteString(ii.HumanId())
		buf.WriteString("\n")
	}

	if is != nil {
		buf.WriteString(is.String())
		buf.WriteString("\n")
	}
	DebugInfo.WriteFile("hook-PostRefresh", buf.Bytes())
	return HookActionContinue, nil
}

func (*DebugHook) PreImportState(ii *InstanceInfo, s string) (HookAction, error) {
	var buf bytes.Buffer
	if ii != nil {
		buf.WriteString(ii.HumanId())
		buf.WriteString("\n")
	}
	buf.WriteString(s + "\n")

	DebugInfo.WriteFile("hook-PreImportState", buf.Bytes())
	return HookActionContinue, nil
}

func (*DebugHook) PostImportState(ii *InstanceInfo, iss []*InstanceState) (HookAction, error) {
	var buf bytes.Buffer

	if ii != nil {
		buf.WriteString(ii.HumanId() + "\n")
	}

	for _, is := range iss {
		if is != nil {
			buf.WriteString(is.String() + "\n")
		}
	}
	DebugInfo.WriteFile("hook-PostImportState", buf.Bytes())
	return HookActionContinue, nil
}

// skip logging this for now, since it could be huge
func (*DebugHook) PostStateUpdate(*State) (HookAction, error) {
	return HookActionContinue, nil
}
