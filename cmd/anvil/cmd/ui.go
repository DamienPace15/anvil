package cmd

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/pulumi/pulumi/sdk/v3/go/auto/events"
	"github.com/pulumi/pulumi/sdk/v3/go/common/apitype"
	"golang.org/x/term"
)

// ── Terminal detection ──

func isTTY() bool {
	return term.IsTerminal(int(os.Stdout.Fd()))
}

// ── ANSI colours ──

const (
	colorReset  = "\033[0m"
	colorRed    = "\033[31m"
	colorGreen  = "\033[32m"
	colorYellow = "\033[33m"
	colorCyan   = "\033[36m"
	colorBold   = "\033[1m"
	colorDim    = "\033[2m"
)

func green(s string) string  { return colorGreen + s + colorReset }
func red(s string) string    { return colorRed + s + colorReset }
func yellow(s string) string { return colorYellow + s + colorReset }
func cyan(s string) string   { return colorCyan + s + colorReset }
func bold(s string) string   { return colorBold + s + colorReset }
func dim(s string) string    { return colorDim + s + colorReset }

func plainIcon(op string, failed bool) string {
	if failed {
		return "[FAIL]"
	}
	switch op {
	case "update", "replace", "create-replacement":
		return "[~]"
	default:
		return "[ok]"
	}
}

// ── Spinner ──

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

type spinner struct {
	stop    chan struct{}
	stopped chan struct{}
	line    string
}

func newSpinner(line string) *spinner {
	s := &spinner{
		stop:    make(chan struct{}),
		stopped: make(chan struct{}),
		line:    line,
	}
	go s.run()
	return s
}

func (s *spinner) run() {
	defer close(s.stopped)
	i := 0
	ticker := time.NewTicker(80 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-s.stop:
			fmt.Printf("\r\033[K")
			return
		case <-ticker.C:
			frame := cyan(spinnerFrames[i%len(spinnerFrames)])
			fmt.Printf("\r  %s %s", frame, s.line)
			i++
		}
	}
}

func (s *spinner) finish() {
	close(s.stop)
	<-s.stopped
}

// ── Banner ──

func printBanner() {
	if isTTY() {
		fmt.Println()
		fmt.Println(green("      ┌────────────────────┐"))
		fmt.Println(green("      │   A  N  V  I  L    │"))
		fmt.Println(green("      └──┬──────────────┬──┘"))
		fmt.Println(green("         │██████████████│"))
		fmt.Println(green("      ┌──┴──────────────┴──┐"))
		fmt.Println(green("      │ secure by default  │"))
		fmt.Println(green("     ─┴────────────────────┴─"))
	} else {
		fmt.Println()
		fmt.Println("  ANVIL — secure by default")
	}
	fmt.Println()
	if isTTY() {
		time.Sleep(150 * time.Millisecond)
	}
}

// ── Duration formatting ──

func formatDuration(d time.Duration) string {
	if d < 10*time.Second {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	mins := int(d.Minutes())
	secs := int(d.Seconds()) % 60
	return fmt.Sprintf("%dm %ds", mins, secs)
}

// ── Resource tracking ──

type trackedResource struct {
	urn       string
	typeName  string
	name      string
	op        string
	startTime time.Time
	endTime   time.Time
	failed    bool
}

// ── URN parsing ──

func parseURN(urn string) (typeName string, resourceName string) {
	parts := strings.Split(urn, "::")
	if len(parts) < 4 {
		return urn, urn
	}
	typeName = parts[2]
	resourceName = parts[3]
	return
}

func isAnvilComponent(typeToken string) bool {
	// Child resources have compound tokens: "anvil:aws:Bucket$aws:s3/bucket:Bucket"
	// Check the leaf type (after last $), not the parent prefix.
	if idx := strings.LastIndex(typeToken, "$"); idx != -1 {
		typeToken = typeToken[idx+1:]
	}
	return strings.HasPrefix(typeToken, "anvil:")
}

func isInternalResource(typeToken string, verbose bool) bool {
	if strings.HasPrefix(typeToken, "pulumi:pulumi:") {
		return true
	}
	if strings.HasPrefix(typeToken, "pulumi:providers:") {
		return true
	}
	if !verbose && !isAnvilComponent(typeToken) {
		return true
	}
	return false
}

func shortTypeName(typeToken string) string {
	// For compound tokens, use the leaf type
	if idx := strings.LastIndex(typeToken, "$"); idx != -1 {
		typeToken = typeToken[idx+1:]
	}
	parts := strings.Split(typeToken, ":")
	if len(parts) >= 3 {
		return parts[len(parts)-1]
	}
	return typeToken
}

// ── Op helpers ──

func opVerb(op string) string {
	switch op {
	case "create", "create-replacement":
		return "created"
	case "update":
		return "updated"
	case "delete", "delete-replaced":
		return "deleted"
	case "replace":
		return "replaced"
	case "same", "read":
		return ""
	default:
		return op
	}
}

func opSpinnerVerb(op string) string {
	switch op {
	case "create", "create-replacement":
		return "creating..."
	case "update":
		return "updating..."
	case "delete", "delete-replaced":
		return "deleting..."
	case "replace":
		return "replacing..."
	default:
		return "processing..."
	}
}

func opIcon(op string, failed bool) string {
	if failed {
		return red("✘")
	}
	switch op {
	case "update", "replace", "create-replacement":
		return yellow("~")
	default:
		return green("✔")
	}
}

// ── Event handler ──

type EventHandler struct {
	verbose   bool
	resources map[string]*trackedResource
	errors    []diagError
	spinner   *spinner
	startTime time.Time
}

type diagError struct {
	urn     string
	message string
}

func NewEventHandler(verbose bool) *EventHandler {
	return &EventHandler{
		verbose:   verbose,
		resources: make(map[string]*trackedResource),
		startTime: time.Now(),
	}
}

// HandleEvent processes a single engine event.
// events.EngineEvent embeds apitype.EngineEvent, so sub-event fields
// (ResourcePreEvent, ResOutputsEvent, etc.) are promoted from apitype.
func (h *EventHandler) HandleEvent(event events.EngineEvent) {
	switch {
	case event.ResourcePreEvent != nil:
		h.handleResourcePre(event.ResourcePreEvent)
	case event.ResOutputsEvent != nil:
		h.handleResOutputs(event.ResOutputsEvent)
	case event.ResOpFailedEvent != nil:
		h.handleResOpFailed(event.ResOpFailedEvent)
	case event.DiagnosticEvent != nil:
		h.handleDiagnostic(event.DiagnosticEvent)
	case event.SummaryEvent != nil:
		// We build our own summary, ignore Pulumi's.
	}
}

func (h *EventHandler) handleResourcePre(e *apitype.ResourcePreEvent) {
	meta := e.Metadata
	typeName, resourceName := parseURN(meta.URN)

	tr := &trackedResource{
		urn:       meta.URN,
		typeName:  typeName,
		name:      resourceName,
		op:        string(meta.Op),
		startTime: time.Now(),
	}
	h.resources[meta.URN] = tr

	if isInternalResource(typeName, h.verbose) {
		return
	}
	if string(meta.Op) == "same" || string(meta.Op) == "read" {
		return
	}

	displayName := shortTypeName(typeName)
	indent := "  "
	if h.verbose && !isAnvilComponent(typeName) {
		indent = "    "
	}

	line := fmt.Sprintf("%-14s %s", bold(displayName), dim(opSpinnerVerb(string(meta.Op))))

	if isTTY() {
		if h.spinner != nil {
			h.spinner.finish()
		}
		h.spinner = newSpinner(indent + line)
	}
}

func (h *EventHandler) handleResOutputs(e *apitype.ResOutputsEvent) {
	meta := e.Metadata
	tr, ok := h.resources[meta.URN]
	if !ok {
		return
	}
	tr.endTime = time.Now()

	if isInternalResource(tr.typeName, h.verbose) {
		return
	}
	if tr.op == "same" || tr.op == "read" {
		return
	}

	if isTTY() && h.spinner != nil {
		h.spinner.finish()
		h.spinner = nil
	}

	h.printResourceLine(tr)
}

func (h *EventHandler) handleResOpFailed(e *apitype.ResOpFailedEvent) {
	meta := e.Metadata
	tr, ok := h.resources[meta.URN]
	if !ok {
		return
	}
	tr.failed = true
	tr.endTime = time.Now()

	if isInternalResource(tr.typeName, h.verbose) {
		return
	}

	if isTTY() && h.spinner != nil {
		h.spinner.finish()
		h.spinner = nil
	}

	h.printResourceLine(tr)
}

func (h *EventHandler) handleDiagnostic(e *apitype.DiagnosticEvent) {
	if e.Severity == "error" {
		h.errors = append(h.errors, diagError{
			urn:     e.URN,
			message: mapError(strings.TrimSpace(e.Message)),
		})
	}
}

func (h *EventHandler) printResourceLine(tr *trackedResource) {
	displayName := shortTypeName(tr.typeName)
	duration := tr.endTime.Sub(tr.startTime)

	indent := "  "
	if h.verbose && !isAnvilComponent(tr.typeName) {
		indent = "    "
	}

	if tr.failed {
		if isTTY() {
			fmt.Printf("%s%s %-14s %s\n", indent, red("✘"), bold(displayName), red("failed"))
		} else {
			fmt.Printf("%s[FAIL] %-14s failed\n", indent, displayName)
		}
		return
	}

	verb := opVerb(tr.op)
	timing := fmt.Sprintf("(%s)", formatDuration(duration))

	if isTTY() {
		icon := opIcon(tr.op, false)
		fmt.Printf("%s%s %-14s %-10s %s\n", indent, icon, bold(displayName), verb, dim(timing))
	} else {
		icon := plainIcon(tr.op, false)
		fmt.Printf("%s%s %-14s %-10s %s\n", indent, icon, displayName, verb, timing)
	}
}

func (h *EventHandler) PrintSummary(command string, stage string) {
	totalDuration := time.Since(h.startTime)

	if h.spinner != nil {
		h.spinner.finish()
		h.spinner = nil
	}

	if isTTY() {
		time.Sleep(100 * time.Millisecond)
	}

	counts := map[string]int{}
	failed := 0
	for _, tr := range h.resources {
		if isInternalResource(tr.typeName, false) {
			continue
		}
		if tr.op == "same" || tr.op == "read" || tr.op == "" {
			continue
		}
		if tr.failed {
			failed++
			continue
		}
		switch tr.op {
		case "create", "create-replacement":
			counts["created"]++
		case "update":
			counts["updated"]++
		case "delete", "delete-replaced":
			counts["deleted"]++
		case "replace":
			counts["replaced"]++
		}
	}

	total := 0
	for _, c := range counts {
		total += c
	}

	fmt.Println()

	if failed > 0 || len(h.errors) > 0 {
		h.printFailureSummary(command, stage, counts, total, failed)
		return
	}

	if total == 0 {
		fmt.Println("  No changes. Infrastructure is up to date.")
		return
	}

	summaryParts := []string{}
	if c, ok := counts["created"]; ok {
		summaryParts = append(summaryParts, fmt.Sprintf("%d created", c))
	}
	if c, ok := counts["updated"]; ok {
		summaryParts = append(summaryParts, fmt.Sprintf("%d updated", c))
	}
	if c, ok := counts["replaced"]; ok {
		summaryParts = append(summaryParts, fmt.Sprintf("%d replaced", c))
	}
	if c, ok := counts["deleted"]; ok {
		summaryParts = append(summaryParts, fmt.Sprintf("%d deleted", c))
	}

	summary := strings.Join(summaryParts, ", ")

	commandVerb := "Deploy"
	if command == "destroy" {
		commandVerb = "Destroy"
	}

	if isTTY() {
		fmt.Printf("  %s %s complete — %s in %s\n",
			green("✔"), commandVerb, summary, formatDuration(totalDuration))
	} else {
		fmt.Printf("  %s complete — %s in %s\n",
			commandVerb, summary, formatDuration(totalDuration))
	}

	fmt.Println()
	switch command {
	case "deploy":
		if isTTY() {
			fmt.Println(dim("  Run `anvil preview` to check for drift"))
		} else {
			fmt.Println("  Run `anvil preview` to check for drift")
		}
	case "destroy":
		if isTTY() {
			fmt.Printf(dim("  Stage \"%s\" is now empty\n"), stage)
		} else {
			fmt.Printf("  Stage \"%s\" is now empty\n", stage)
		}
	}
}

func (h *EventHandler) printFailureSummary(command, stage string, counts map[string]int, total, failed int) {
	commandVerb := "Deploy"
	rerunCmd := "anvil deploy"
	if command == "destroy" {
		commandVerb = "Destroy"
		rerunCmd = "anvil destroy --yes"
	}

	parts := []string{}
	if total > 0 {
		for verb, c := range counts {
			parts = append(parts, fmt.Sprintf("%d %s", c, verb))
		}
	}
	parts = append(parts, fmt.Sprintf("%d failed", failed))
	summary := strings.Join(parts, ", ")

	if isTTY() {
		fmt.Printf("  %s %s failed — %s\n", red("✘"), commandVerb, summary)
	} else {
		fmt.Printf("  %s failed — %s\n", commandVerb, summary)
	}

	if len(h.errors) > 0 {
		fmt.Println()
		if isTTY() {
			fmt.Println(red("  Errors:"))
		} else {
			fmt.Println("  Errors:")
		}
		fmt.Println()

		for _, e := range h.errors {
			displayName := ""
			if tr, ok := h.resources[e.urn]; ok && isAnvilComponent(tr.typeName) {
				displayName = fmt.Sprintf("    %s  %s", shortTypeName(tr.typeName), tr.name)
			} else if e.urn != "" {
				_, name := parseURN(e.urn)
				displayName = fmt.Sprintf("    %s", name)
			}

			if displayName != "" {
				if isTTY() {
					fmt.Println(bold(displayName))
				} else {
					fmt.Println(displayName)
				}
			}
			fmt.Printf("    %s\n\n", strings.TrimSpace(e.message))
		}

		if isTTY() {
			fmt.Printf(dim("  Fix the errors above, then run `%s` again.\n"), rerunCmd)
			if command == "deploy" {
				fmt.Println(dim("  Successfully created resources will not be recreated."))
			} else if command == "destroy" {
				fmt.Println(dim("  Successfully deleted resources will not be re-deleted."))
			}
		} else {
			fmt.Printf("  Fix the errors above, then run `%s` again.\n", rerunCmd)
			if command == "deploy" {
				fmt.Println("  Successfully created resources will not be recreated.")
			} else if command == "destroy" {
				fmt.Println("  Successfully deleted resources will not be re-deleted.")
			}
		}
	}
}

func (h *EventHandler) HasErrors() bool {
	return len(h.errors) > 0
}
