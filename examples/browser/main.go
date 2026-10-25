// Command browser demonstrates an optional capability selected by imports.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/chainreactors/cyber/agent/provider"
	"github.com/chainreactors/cyber/core/extension"
	coretool "github.com/chainreactors/cyber/core/tool"
	browserext "github.com/chainreactors/cyber/exts/browser"
	"github.com/chainreactors/cyber/pkg/harness"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() (runErr error) {
	directory, err := os.Getwd()
	if err != nil {
		return err
	}
	entries, err := harness.BaseExtensions(harness.BaseConfig{Directory: directory, Provider: provider.StartupConfig{Mode: provider.StartupDisabled}})
	if err != nil {
		return err
	}
	feature, err := browserext.New(directory, "default")
	if err != nil {
		return err
	}
	entries = append(entries, feature)
	var commands coretool.CommandExecutor
	entries = append(entries, extension.Func{LoadFunc: func(scope *extension.Scope) error {
		var err error
		commands, err = extension.Use[coretool.CommandExecutor](scope)
		return err
	}})
	set, err := extension.New(entries...)
	if err != nil {
		return err
	}
	defer func() { runErr = errors.Join(runErr, set.Close(context.Background())) }()
	if err = set.Load(context.Background()); err != nil {
		return err
	}
	fmt.Println(commands.Names())
	return nil
}
