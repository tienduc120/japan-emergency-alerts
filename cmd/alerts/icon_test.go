package main

import (
	"bytes"
	"encoding/binary"
	"os"
	"testing"
)

// An .ico file is a six-byte header, then sixteen bytes per image saying how big it is and where it starts.
const (
	iconHeaderBytes = 6
	iconEntryBytes  = 16
)

func TestTheWindowsProgramCarriesTheIconOfTheSettingsPage(t *testing.T) {
	t.Parallel()
	icon, err := os.ReadFile("../../internal/web/favicon.ico")
	if err != nil {
		t.Fatal(err)
	}
	resources, err := os.ReadFile("rsrc_windows_amd64.syso")
	if err != nil {
		t.Fatalf("no Windows resource file: %v; build it from the icon as AGENTS.md says", err)
	}

	count := int(binary.LittleEndian.Uint16(icon[4:6]))
	if count == 0 {
		t.Fatal("the page's icon has no images")
	}
	for i := range count {
		entry := icon[iconHeaderBytes+i*iconEntryBytes:]
		size := int(binary.LittleEndian.Uint32(entry[8:12]))
		offset := int(binary.LittleEndian.Uint32(entry[12:16]))
		if !bytes.Contains(resources, icon[offset:offset+size]) {
			t.Errorf(
				"image %d of the page's icon is missing from the program's resources: rebuild the resource file",
				i,
			)
		}
	}
}
