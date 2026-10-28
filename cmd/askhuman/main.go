// Command askhuman generates and serves interactive HTML5 questionnaires (templ)
// for structured human-in-the-loop requirement elicitation and design arbitration.
package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/hazyhaar/askhuman/pkg/questionnaire"
)

func main() {
	os.Exit(Run(os.Args[1:], os.Stdout, os.Stderr))
}

// Run executes the requested askhuman subcommand and returns the exit code (0, 1, or 2).
func Run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printUsage(stderr)
		return 1
	}
	sub := args[0]
	rest := args[1:]

	switch sub {
	case "render":
		return cmdRender(rest, stdout, stderr)
	case "serve":
		return cmdServe(rest, stdout, stderr)
	case "format-md":
		return cmdFormatMD(rest, stdout, stderr)
	case "validate-spec":
		return cmdValidateSpec(rest, stdout, stderr)
	default:
		fmt.Fprintf(stderr, "askhuman: unknown subcommand %q\n", sub)
		printUsage(stderr)
		return 1
	}
}

func printUsage(w io.Writer) {
	fmt.Fprintln(w, `Usage: askhuman <subcommand> [options]
Subcommands:
  render -in <spec.json> -out <askhuman.html> [-endpoint /submit]
      Generate a standalone interactive HTML5 page via templ.
  serve -in <spec.json> [-addr 127.0.0.1:0] [-out-json <answers.json>] [-out-md <decisions.md>] [-once]
      Serve the questionnaire over local HTTP and atomically persist answers.
  format-md -spec <spec.json> -answers <answers.json> -out <decisions.md>
      Convert a JSON answers file into a structured Markdown report.
  validate-spec -in <spec.json>
      Validate the structural integrity of a questionnaire specification file.`)
}

func cmdValidateSpec(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("validate-spec", flag.ContinueOnError)
	fs.SetOutput(stderr)
	inPath := fs.String("in", "", "path to questionnaire JSON specification")
	if err := fs.Parse(args); err != nil || *inPath == "" {
		fmt.Fprintln(stderr, "usage: askhuman validate-spec -in <spec.json>")
		return 1
	}
	spec, err := questionnaire.LoadSpecFile(*inPath)
	if err != nil {
		fmt.Fprintf(stderr, "askhuman: %v\n", err)
		return 2
	}
	fmt.Fprintf(stdout, "Valid spec: project=%s questions=%d\n", spec.Project, len(spec.Questions))
	return 0
}

func cmdRender(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("render", flag.ContinueOnError)
	fs.SetOutput(stderr)
	inPath := fs.String("in", "", "path to questionnaire JSON specification")
	outPath := fs.String("out", "", "path to output HTML5 file")
	endpoint := fs.String("endpoint", "/submit", "optional POST submission endpoint")
	if err := fs.Parse(args); err != nil || *inPath == "" || *outPath == "" {
		fmt.Fprintln(stderr, "usage: askhuman render -in <spec.json> -out <askhuman.html> [-endpoint /submit]")
		return 1
	}
	spec, err := questionnaire.LoadSpecFile(*inPath)
	if err != nil {
		fmt.Fprintf(stderr, "askhuman: %v\n", err)
		return 2
	}
	var buf bytes.Buffer
	if err := questionnaire.RenderHTML(context.Background(), spec, *endpoint, &buf); err != nil {
		fmt.Fprintf(stderr, "askhuman: render HTML: %v\n", err)
		return 2
	}
	if err := questionnaire.WriteAtomicFile(*outPath, buf.Bytes(), 0o644); err != nil {
		fmt.Fprintf(stderr, "askhuman: write %s: %v\n", *outPath, err)
		return 1
	}
	fmt.Fprintf(stdout, "Interactive HTML page generated: %s (%d bytes)\n", *outPath, buf.Len())
	return 0
}

func cmdFormatMD(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("format-md", flag.ContinueOnError)
	fs.SetOutput(stderr)
	specPath := fs.String("spec", "", "path to questionnaire JSON specification")
	ansPath := fs.String("answers", "", "path to JSON answers file")
	outPath := fs.String("out", "", "path to output Markdown file")
	if err := fs.Parse(args); err != nil || *specPath == "" || *ansPath == "" || *outPath == "" {
		fmt.Fprintln(stderr, "usage: askhuman format-md -spec <spec.json> -answers <answers.json> -out <decisions.md>")
		return 1
	}
	spec, err := questionnaire.LoadSpecFile(*specPath)
	if err != nil {
		fmt.Fprintf(stderr, "askhuman: %v\n", err)
		return 2
	}
	f, err := os.Open(*ansPath)
	if err != nil {
		fmt.Fprintf(stderr, "askhuman: open %s: %v\n", *ansPath, err)
		return 1
	}
	defer f.Close()
	sub, err := questionnaire.ParseSubmission(f)
	if err != nil {
		fmt.Fprintf(stderr, "askhuman: %v\n", err)
		return 2
	}
	if err := questionnaire.ValidateSubmission(spec, sub); err != nil {
		fmt.Fprintf(stderr, "askhuman: %v\n", err)
		return 2
	}
	absOut, err := filepath.Abs(*outPath)
	if err != nil {
		fmt.Fprintf(stderr, "askhuman: %v\n", err)
		return 1
	}
	md := questionnaire.FormatMarkdownReport(spec, sub, absOut)
	if err := questionnaire.WriteAtomicFile(absOut, []byte(md), 0o644); err != nil {
		fmt.Fprintf(stderr, "askhuman: write %s: %v\n", absOut, err)
		return 1
	}
	fmt.Fprintf(stdout, "Markdown report generated: %s\n", absOut)
	return 0
}

func cmdServe(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(stderr)
	inPath := fs.String("in", "", "path to questionnaire JSON specification")
	addr := fs.String("addr", "127.0.0.1:0", "local listen address")
	outJSON := fs.String("out-json", "", "path to persist JSON answers")
	outMD := fs.String("out-md", "", "path to persist Markdown report")
	once := fs.Bool("once", false, "shut down server after first valid submission")
	timeout := fs.Duration("timeout", 60*time.Minute, "maximum lifetime before automatic shutdown (e.g. 60m)")
	if err := fs.Parse(args); err != nil || *inPath == "" {
		fmt.Fprintln(stderr, "usage: askhuman serve -in <spec.json> [-addr 127.0.0.1:0] [-out-json <f.json>] [-out-md <f.md>] [-once] [-timeout 60m]")
		return 1
	}
	spec, err := questionnaire.LoadSpecFile(*inPath)
	if err != nil {
		fmt.Fprintf(stderr, "askhuman: %v\n", err)
		return 2
	}
	specDir := filepath.Dir(*inPath)
	if *outJSON == "" {
		*outJSON = filepath.Join(specDir, "answers.json")
	}
	if *outMD == "" {
		*outMD = filepath.Join(specDir, "decisions.md")
	}

	doneCh := make(chan struct{}, 1)
	handler, err := questionnaire.NewHandler(questionnaire.ServerOptions{
		Spec:        spec,
		OutJSONPath: *outJSON,
		OutMDPath:   *outMD,
		OnSubmitted: func(_ questionnaire.QuestionnaireSubmission) {
			if *once {
				select {
				case doneCh <- struct{}{}:
				default:
				}
			}
		},
	})
	if err != nil {
		fmt.Fprintf(stderr, "askhuman: %v\n", err)
		return 2
	}

	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		fmt.Fprintf(stderr, "askhuman: listen %s: %v\n", *addr, err)
		return 1
	}
	srv := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}

	fmt.Fprintf(stdout, "askhuman listening on http://%s\n", ln.Addr().String())
	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Serve(ln)
	}()

	var timeoutCh <-chan time.Time
	if *timeout > 0 {
		timer := time.NewTimer(*timeout)
		defer timer.Stop()
		timeoutCh = timer.C
	}

	select {
	case <-doneCh:
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
		return 0
	case <-timeoutCh:
		fmt.Fprintf(stderr, "askhuman: timeout (%s) reached, shutting down.\n", (*timeout).String())
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
		return 0
	case srvErr := <-errCh:
		if srvErr != nil && srvErr != http.ErrServerClosed {
			fmt.Fprintf(stderr, "askhuman: server: %v\n", srvErr)
			return 1
		}
		return 0
	}
}
