package terraform

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"io/ioutil"
	"regexp"
	"testing"
)

// debugInfo should be safe when nil
func TestDebugInfo_nil(t *testing.T) {
	var d *debugInfo

	d.SetPhase("none")
	d.WriteGraph(nil)
	d.WriteFile("none", nil)
	d.Close()
}

func TestDebugInfo_basicFile(t *testing.T) {
	var w bytes.Buffer
	debug, err := newDebugInfo("test-debug-info", &w)
	if err != nil {
		t.Fatal(err)
	}
	debug.SetPhase("test")

	fileData := map[string][]byte{
		"file1": []byte("file 1 data"),
		"file2": []byte("file 2 data"),
		"file3": []byte("file 3 data"),
	}

	for f, d := range fileData {
		err = debug.WriteFile(f, d)
		if err != nil {
			t.Fatal(err)
		}
	}

	err = debug.Close()
	if err != nil {
		t.Fatal(err)
	}

	gz, err := gzip.NewReader(&w)
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}

		// get the filename part of the archived file
		name := regexp.MustCompile(`\w+$`).FindString(hdr.Name)
		data := fileData[name]

		delete(fileData, name)

		tarData, err := ioutil.ReadAll(tr)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(data, tarData) {
			t.Fatalf("got '%s' for file '%s'", tarData, name)
		}
	}

	for k := range fileData {
		t.Fatalf("didn't find file %s", k)
	}
}

// verify that no hooks panic on nil input
func TestDebugHook_nilArgs(t *testing.T) {
	// make sure debug isn't nil, so the hooks try to execute
	var w bytes.Buffer
	var err error
	debug, err = newDebugInfo("test-debug-info", &w)
	if err != nil {
		t.Fatal(err)
	}

	var h DebugHook
	h.PostApply(nil, nil, nil)
	h.PostDiff(nil, nil)
	h.PostImportState(nil, nil)
	h.PostProvision(nil, "")
	h.PostProvisionResource(nil, nil)
	h.PostRefresh(nil, nil)
	h.PostStateUpdate(nil)
	h.PreApply(nil, nil, nil)
	h.PreDiff(nil, nil)
	h.PreImportState(nil, "")
	h.PreProvision(nil, "")
	h.PreProvisionResource(nil, nil)
	h.PreRefresh(nil, nil)
	h.ProvisionOutput(nil, "", "")
}
