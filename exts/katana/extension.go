package katana

import (
	"context"
	"embed"

	"github.com/chainreactors/cyber/core/egress"
	"github.com/chainreactors/cyber/core/events"
	"github.com/chainreactors/cyber/core/extension"
	"github.com/chainreactors/cyber/core/telemetry"
	"github.com/chainreactors/cyber/tools/files"
	"github.com/chainreactors/cyber/tools/katana"
)

//go:embed katana.md
var docs embed.FS

type Extension struct{ files *files.Files }

func New() *Extension { return &Extension{} }
func (e *Extension) Load(scope *extension.Scope) error {
	logger, err := extension.Use[telemetry.Logger](scope)
	if err != nil {
		return err
	}
	endpoint, err := extension.Use[egress.Endpoint](scope)
	if err != nil {
		return err
	}
	stream, err := extension.Use[*events.Stream](scope)
	if err != nil {
		return err
	}
	filesCap, err := extension.Use[*files.Files](scope)
	if err != nil {
		return err
	}
	if err = filesCap.Mount("cyber://skills/katana/", docs); err != nil {
		return err
	}
	e.files = filesCap
	return extension.Add(scope, katana.NewCommand(logger, endpoint.ProxyURL(), stream))
}
func (e *Extension) Close(ctx context.Context) error {
	if e.files == nil {
		return nil
	}
	err := e.files.Unmount(ctx, "cyber://skills/katana/")
	if err == nil {
		e.files = nil
	}
	return err
}
