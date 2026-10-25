package main

import (
	"slices"
	"testing"
)

func scannerNames() []string {
	names := productScannerNames()
	slices.Sort(names)
	return names
}

func TestCLIParserDeclaresAvailableScanners(t *testing.T) {
	var cli cliOptions
	parser := newCLIParser(&cli, 0)
	for _, name := range scannerNames() {
		if parser.Find(name) == nil {
			t.Errorf("available scanner %q is missing from CLI commands", name)
		}
	}
}
