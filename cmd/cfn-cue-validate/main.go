// cfn-cue-validate validates one normalized JSON CloudFormation template.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/cueformation/cfn-cue/internal/validate"
)

func main() {
	schemas := flag.String("schemas", "generated", "generated CUE module directory")
	region := flag.String("region", "us-east-1", "region to check")
	input := flag.String("template", "", "normalized JSON template file")
	resource := flag.String("resource", "", "check only this logical resource, omitting template-level checks")
	deadline := flag.Duration("timeout", 30*time.Second, "process deadline, including CUE evaluation")
	flag.Parse()
	if *input == "" || *deadline <= 0 {
		fmt.Fprintln(os.Stderr, "-template is required and -timeout must be positive")
		os.Exit(2)
	}
	start := time.Now()
	finished := make(chan validate.Result, 1)
	go func() {
		file, err := os.Open(*input)
		if err != nil {
			finished <- validate.Result{Status: "error", Message: err.Error(), Region: *region}
			return
		}
		defer file.Close()
		data, err := io.ReadAll(io.LimitReader(file, (8<<20)+1))
		if len(data) > 8<<20 {
			finished <- validate.Result{Status: "error", Message: "template must be at most 8 MiB", Region: *region}
			return
		}
		if err != nil {
			finished <- validate.Result{Status: "error", Message: err.Error(), Region: *region}
			return
		}
		finished <- validate.Check(data, validate.Options{Schemas: *schemas, Region: *region, Resource: *resource})
	}()
	var result validate.Result
	select {
	case result = <-finished:
	case <-time.After(*deadline):
		result = validate.Result{Status: "timeout", Phase: "deadline", Region: *region, Resource: *resource, ElapsedMS: time.Since(start).Milliseconds(), Message: "CUE evaluation exceeded the process deadline"}
	}
	_ = json.NewEncoder(os.Stdout).Encode(result)
	switch result.Status {
	case "valid":
	case "timeout":
		os.Exit(124)
	default:
		os.Exit(1)
	}
}
