package golang_test

import (
	"reflect"
	"testing"

	"github.com/muralx/codedoc/internal/extract"
	"github.com/muralx/codedoc/internal/extract/golang"
)

const apiSrc = `// Package cmd builds command line tools.
package cmd

import "fmt"

// Command is one command of a tool.
type Command struct {
	// Use is the one-line usage.
	Use  string
	args []string
	Embedded
}

type hidden struct{ Visible int }

// Execute runs the command.
func (c *Command) Execute() error { fmt.Println(c.Use); return nil }

func (c *Command) parse() {}

func (h hidden) Exported() {}

// Runner runs things.
type Runner interface {
	// Run runs.
	Run() error
	stop()
}

// ErrUsage is returned for bad flags.
var ErrUsage = fmt.Errorf("usage")

const maxDepth, MaxArgs = 3, 9

// New returns a command.
func New(use string) *Command { return &Command{Use: use} }

func helper() {}
`

func TestExtractAPIKeepsExportedIdentifiersOnly(t *testing.T) {
	res, err := golang.New().ExtractAPI("command.go", []byte(apiSrc))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	docs := map[string]string{}
	for _, s := range res.Symbols {
		got = append(got, s.Qualified)
		if s.Doc != nil {
			docs[s.Qualified] = s.Doc.Text
		}
	}
	want := []string{"Command", "Command.Use", "Command.Embedded", "Command.Execute", "Runner", "Runner.Run", "ErrUsage",
		"MaxArgs", "New"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %v\nwant %v", got, want)
	}
	wantDocs := map[string]string{"Command": "Command is one command of a tool.", "Command.Execute": "Execute runs the command.",
		"Command.Use": "Use is the one-line usage.", "Runner": "Runner runs things.", "Runner.Run": "Run runs.",
		"ErrUsage": "ErrUsage is returned for bad flags.", "New": "New returns a command."}
	if !reflect.DeepEqual(docs, wantDocs) {
		t.Errorf("docs: %v", docs)
	}
	if res.ModuleDoc == nil || res.ModuleDoc.Text != "Package cmd builds command line tools." {
		t.Errorf("package doc: %+v", res.ModuleDoc)
	}
	if res.ModuleName != "cmd" || len(res.References)+len(res.Imports) != 0 {
		t.Errorf("API mode extracts the package name and no references or imports: %q %+v %+v", res.ModuleName,
			res.References, res.Imports)
	}
	if _, ok := extract.APIFor("go"); !ok {
		t.Error("go extractor has no API mode")
	}
}
