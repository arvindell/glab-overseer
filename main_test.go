package main

import (
	"bytes"
	"flag"
	"os"
	"strings"
	"testing"
)

func TestTokenEnvironmentDefaultIsNotPrintedInHelp(t *testing.T) {
	previousFlags, previousArgs := flag.CommandLine, os.Args
	defer func() { flag.CommandLine, os.Args = previousFlags, previousArgs }()
	flag.CommandLine = flag.NewFlagSet("glab-overseer", flag.ContinueOnError)
	var output bytes.Buffer
	flag.CommandLine.SetOutput(&output)
	os.Args = []string{"glab-overseer"}
	t.Setenv("GITLAB_PROJECT", "example/project")
	t.Setenv("GITLAB_TOKEN", "private-test-token-not-for-help")
	cfg, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Token != "private-test-token-not-for-help" {
		t.Fatal("environment token was not loaded")
	}
	flag.PrintDefaults()
	if strings.Contains(output.String(), cfg.Token) {
		t.Fatal("help exposed the configured GitLab token")
	}
}
