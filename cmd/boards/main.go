// Command boards runs the Git-Hospital benchmark: it puts a tool in front of
// known defects and clean twins, and scores what it says.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/Git-Hospital/boards/internal/adapter"
	"github.com/Git-Hospital/boards/internal/casefile"
	"github.com/Git-Hospital/boards/internal/dummy"
	"github.com/Git-Hospital/boards/internal/execx"
	"github.com/Git-Hospital/boards/internal/report"
	"github.com/Git-Hospital/boards/internal/runner"
	"github.com/Git-Hospital/boards/internal/score"
	"github.com/Git-Hospital/boards/internal/testbed"
)

// version is set at release time with -ldflags.
var version = "dev"

// Exit codes, the same as Helmbulance's (D23).
const (
	exitPass         = 0
	exitFail         = 1
	exitUsage        = 2
	exitInconclusive = 3
)

const usage = `usage: boards <command> [flags]

commands:
  validate   check every case.yaml against the schema and the case rules
  list       list the cases
  plan       print what a case would run, without running anything
  run        run a tool over cases and score it
  version    print the boards version

Run 'boards <command> -h' for the flags of a command.
`

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, usage)
		return exitUsage
	}
	switch args[0] {
	case "validate":
		return cmdValidate(args[1:])
	case "list":
		return cmdList(args[1:])
	case "plan":
		return cmdPlan(args[1:])
	case "run":
		return cmdRun(args[1:])
	case "dummy":
		// Not listed in usage: the dummy tools are adapters of boards itself.
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: boards dummy <oracle|silent|panic> run ...")
			return exitUsage
		}
		if err := dummy.Main(args[1], args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return exitUsage
		}
		return exitPass
	case "version", "--version":
		fmt.Println("boards", version)
		return exitPass
	case "-h", "--help", "help":
		fmt.Print(usage)
		return exitPass
	}
	fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", args[0], usage)
	return exitUsage
}

func loadAll(dir string) ([]*casefile.Case, error) {
	cases, err := casefile.LoadDir(dir)
	if err != nil {
		return nil, err
	}
	var errs []error
	for _, c := range cases {
		if err := casefile.Validate(c); err != nil {
			errs = append(errs, err)
		}
	}
	if len(cases) == 0 && len(errs) == 0 {
		errs = append(errs, fmt.Errorf("no %s found under %s", casefile.FileName, dir))
	}
	return cases, errors.Join(errs...)
}

func cmdValidate(args []string) int {
	fs := flag.NewFlagSet("validate", flag.ContinueOnError)
	dir := fs.String("cases", "cases", "directory holding the cases")
	if fs.Parse(args) != nil {
		return exitUsage
	}
	cases, err := loadAll(*dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitFail
	}
	fmt.Printf("cases OK: %d\n", len(cases))
	return exitPass
}

func cmdList(args []string) int {
	fs := flag.NewFlagSet("list", flag.ContinueOnError)
	dir := fs.String("cases", "cases", "directory holding the cases")
	track := fs.String("track", "", "only this track")
	if fs.Parse(args) != nil {
		return exitUsage
	}
	cases, err := loadAll(*dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitFail
	}
	for _, c := range cases {
		if *track != "" && c.Track != *track {
			continue
		}
		extra := ""
		if len(c.ContaminatedFor) > 0 {
			extra = "  contaminated for " + strings.Join(c.ContaminatedFor, ", ")
		}
		fmt.Printf("%-40s %-7s %-4s %s%s\n", c.ID, c.Kind, c.Set, c.Title, extra)
	}
	return exitPass
}

func cmdPlan(args []string) int {
	fs := flag.NewFlagSet("plan", flag.ContinueOnError)
	dir := fs.String("cases", "cases", "directory holding the cases")
	if fs.Parse(args) != nil || fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: boards plan [--cases DIR] <case-id>")
		return exitUsage
	}
	cases, err := loadAll(*dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitFail
	}
	sel, err := casefile.Select(cases, "", fs.Args())
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitUsage
	}
	c := sel[0]
	fmt.Printf("%s: %s\n", c.ID, c.Title)
	fmt.Printf("twins: %v; settle %s; window %s\n", c.Twins(), c.Settle.Duration, c.Window.Duration)
	printSteps("inject (defect)", c.Inject)
	if c.CleanTwin != nil {
		printSteps("inject (clean twin)", c.CleanTwin.Inject)
	}
	printSteps("reset", c.Reset)
	fmt.Println("oracle:", c.Oracle.Kinds())
	fmt.Println("nothing was run")
	return exitPass
}

func printSteps(title string, steps []casefile.Step) {
	fmt.Println(title + ":")
	if len(steps) == 0 {
		fmt.Println("  (none)")
	}
	for _, s := range steps {
		line := "  " + strings.Join(s.Run, " ")
		if s.Stdin != "" {
			line += "  < stdin (" + fmt.Sprint(strings.Count(s.Stdin, "\n")) + " lines)"
		}
		fmt.Println(line)
	}
}

type multi []string

func (m *multi) String() string     { return strings.Join(*m, ",") }
func (m *multi) Set(v string) error { *m = append(*m, v); return nil }

func cmdRun(args []string) int {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	dir := fs.String("cases", "cases", "directory holding the cases")
	adapterDir := fs.String("adapter", "", "adapter directory of the tool to run (required)")
	track := fs.String("track", "", "only this track")
	var ids multi
	fs.Var(&ids, "case", "run this case (repeatable); default all")
	dry := fs.Bool("dry-run", false, "use the fake testbed: tests boards itself, never a tool")
	out := fs.String("out", "", "output directory (default boards-out/<time>)")
	suite := fs.String("suite", "unreleased", "suite version recorded in the report")
	baseline := fs.String("baseline", "", "report.json to compare with; any case axis worse than it fails the run")
	if fs.Parse(args) != nil {
		return exitUsage
	}
	if *adapterDir == "" {
		fmt.Fprintln(os.Stderr, "run: --adapter is required")
		return exitUsage
	}
	if !*dry {
		// The kind testbed is the next step of v0.2.0 (architecture §1).
		fmt.Fprintln(os.Stderr, "run: only --dry-run exists in this build; the kind testbed is not implemented yet")
		return exitUsage
	}

	tool, err := adapter.Load(*adapterDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitUsage
	}
	all, err := loadAll(*dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitUsage
	}
	cases, err := casefile.Select(all, *track, ids)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitUsage
	}
	var runnable []*casefile.Case
	for _, c := range cases {
		if tool.Supports(c.Track) {
			runnable = append(runnable, c)
		}
	}
	if len(runnable) == 0 {
		fmt.Fprintf(os.Stderr, "run: %v; adapter %s supports tracks %v\n", runner.ErrNoCases, tool.Name, tool.Tracks)
		return exitUsage
	}

	var base *report.Report
	if *baseline != "" {
		if base, err = report.Read(*baseline); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return exitUsage
		}
	}

	outDir := *out
	if outDir == "" {
		outDir = filepath.Join("boards-out", time.Now().UTC().Format("20060102-150405"))
	}
	home := filepath.Join(outDir, "home")
	for _, d := range []string{outDir, home} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return exitUsage
		}
	}
	absHome, _ := filepath.Abs(home)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	tb := testbed.NewFake(execx.BaseEnv(absHome))
	absWork, _ := filepath.Abs(filepath.Join(outDir, "work"))
	rn := runner.New(runner.Config{
		Work: absWork,
		Log:  func(f string, a ...any) { fmt.Fprintf(os.Stderr, f+"\n", a...) },
	}, tb, tool)

	started := time.Now()
	var results []score.Case
	toolVersion := ""
	for _, c := range runnable {
		res := rn.Case(ctx, c)
		results = append(results, res)
		for _, r := range res.Runs {
			if r.Verdict != nil && toolVersion == "" {
				toolVersion = r.Verdict.Tool.Version
			}
		}
		if ctx.Err() != nil {
			break
		}
	}

	rep := report.New(version, *suite, tb.Info(), tool, toolVersion, results, started)
	if err := rep.WriteJSON(filepath.Join(outDir, "report.json")); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitUsage
	}
	md, err := os.Create(filepath.Join(outDir, "report.md"))
	if err == nil {
		err = rep.WriteMarkdown(md)
		md.Close()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitUsage
	}
	_ = rep.WriteTerminal(os.Stdout)
	fmt.Printf("report: %s\n", filepath.Join(outDir, "report.json"))

	if ctx.Err() != nil || rep.Summary.Inconclusive > 0 {
		return exitInconclusive
	}
	if base != nil {
		if regs := rep.Regressions(base); len(regs) > 0 {
			fmt.Println("regressions against the baseline:")
			for _, r := range regs {
				fmt.Println("  " + r)
			}
			return exitFail
		}
	}
	return exitPass
}
