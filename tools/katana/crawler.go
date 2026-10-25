package katana

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"math"

	"sync/atomic"

	browserutil "github.com/chainreactors/cyber/tools/headless"
	"github.com/projectdiscovery/katana/pkg/engine"
	"github.com/projectdiscovery/katana/pkg/engine/headless"
	"github.com/projectdiscovery/katana/pkg/engine/standard"
	katanaoutput "github.com/projectdiscovery/katana/pkg/output"
	katanatypes "github.com/projectdiscovery/katana/pkg/types"
	"github.com/projectdiscovery/katana/pkg/utils/queue"
)

// Crawl is the Katana adapter selected by a distribution's scan composition.
func Crawl(ctx context.Context, seedURL, hostHeader string, depth int, jsMode bool, proxyURL string, emit func(string)) error {
	handleResult := func(r *katanaoutput.Result) {
		if r != nil && r.Request != nil && r.Request.URL != "" {
			emit(r.Request.URL)
		}
	}
	options := &katanatypes.Options{
		Context:                ctx,
		MaxDepth:               depth,
		FieldScope:             "rdn",
		BodyReadSize:           math.MaxInt,
		RateLimit:              150,
		Strategy:               queue.DepthFirst.String(),
		Silent:                 true,
		ScrapeJSResponses:      jsMode,
		ScrapeJSLuiceResponses: jsMode,
		Headless:               jsMode,
		Timeout:                10,
		TimeStable:             1,
		MaxFailureCount:        10,
		PageLoadStrategy:       "heuristic",
		DOMWaitTime:            5,
		Concurrency:            10,
		Parallelism:            10,
		OnResult: func(r katanaoutput.Result) {
			handleResult(&r)
		},
	}
	if hostHeader != "" {
		options.CustomHeaders = []string{"Host: " + hostHeader}
	}
	if proxy := proxyURL; proxy != "" {
		options.Proxy = proxy
	}
	if jsMode {
		binary, err := browserutil.Discover()
		if err != nil {
			return fmt.Errorf("katana browser discovery: %w", err)
		}
		if binary.Path != "" {
			options.SystemChromePath = binary.Path
			options.UseInstalledChrome = true
		}
	}

	// Use instance-owned output and logging: this process hosts other tools, so
	// construction must not reconfigure the global logger.
	crawlerOptions, err := katanatypes.NewCrawlerOptionsWithOutput(
		options,
		&scanResultWriter{onResult: handleResult},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	if err != nil {
		return fmt.Errorf("katana init %s: %w", seedURL, err)
	}
	defer crawlerOptions.Close()

	var crawler engine.Engine
	if jsMode {
		crawler, err = headless.New(crawlerOptions)
	} else {
		crawler, err = standard.New(crawlerOptions)
	}
	if err != nil {
		return fmt.Errorf("katana create %s: %w", seedURL, err)
	}
	defer crawler.Close()

	if err := crawler.Crawl(seedURL); err != nil {
		if ctx.Err() == nil {
			return fmt.Errorf("katana crawl %s: %w", seedURL, err)
		}
	}
	return ctx.Err()
}

type scanResultWriter struct {
	onResult    func(*katanaoutput.Result)
	resultCount atomic.Int64
}

func (w *scanResultWriter) Close() error { return nil }
func (w *scanResultWriter) Write(result *katanaoutput.Result) error {
	if result != nil {
		w.resultCount.Add(1)
	}
	if w.onResult != nil {
		w.onResult(result)
	}
	return nil
}
func (w *scanResultWriter) WriteErr(_ *katanaoutput.Error) error { return nil }
func (w *scanResultWriter) GetResultCount() int64                { return w.resultCount.Load() }
