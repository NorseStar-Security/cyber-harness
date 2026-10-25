//go:build !full

package main

import (
	"github.com/chainreactors/cyber/core/extension"
	scannerext "github.com/chainreactors/cyber/exts/scanner"
)

const hideReconFlags = true

func scannerConfig(config scannerext.Config) scannerext.Config { return config }
func discoveryExtensions(appConfig) []extension.Extension      { return nil }
func discoveryNames() []string                                 { return nil }
func discoveryUsage(string) (string, bool)                     { return "", false }
func discoveryDescription(string) string                       { return "" }
