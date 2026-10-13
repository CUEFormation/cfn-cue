package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"

	"github.com/cueformation/cfn-cue/internal/generate"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "cfn-cue:", err)
		os.Exit(1)
	}
}
func run() error {
	var opt generate.Options
	source := flag.String("source", generate.LatestURL, "enhanced schema HTTPS ZIP URL, local ZIP, or directory with providers/ and resources/")
	flag.StringVar(&opt.Out, "out", "generated", "generated CUE module directory (only managed output may be replaced)")
	flag.StringVar(&opt.Module, "module", "github.com/cueformation/cfn-cue", "generated CUE module path without @version")
	regions := flag.String("regions", "", "comma-separated regions; default: every available region")
	resources := flag.String("resources", "", "comma-separated resource types; default: every available type")
	flag.BoolVar(&opt.IncludeSAM, "include-sam", false, "merge region-independent SAM types into each region")
	flag.BoolVar(&opt.LanguageExtensions, "language-extensions", false, "allow LanguageExtensions intrinsics and require the transform")
	flag.BoolVar(&opt.Strict, "strict", false, "fail without publishing when constraints require approximations")
	flag.Parse()
	if flag.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %v", flag.Args())
	}
	opt.Regions = split(*regions)
	opt.Resources = split(*resources)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	s, err := generate.OpenSource(ctx, *source)
	if err != nil {
		return err
	}
	m, err := generate.Generate(s, opt)
	if err != nil {
		return err
	}
	fmt.Printf("Generated full and type-name packages for %d regions, plus %d shared resource schemas in %s\n", len(m.Regions), len(m.Schemas), opt.Out)
	fmt.Printf("%d conversion diagnostics recorded in manifest.json\n", len(m.Diagnostics))
	return nil
}
func split(s string) []string {
	out := []string{}
	for _, v := range strings.Split(s, ",") {
		v = strings.TrimSpace(v)
		if v != "" {
			out = append(out, v)
		}
	}
	return out
}
