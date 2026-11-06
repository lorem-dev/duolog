package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/lorem-dev/duolog/internal/config"
	"github.com/lorem-dev/duolog/internal/gate"
	"github.com/lorem-dev/duolog/internal/mode"
	"github.com/lorem-dev/duolog/internal/store"
	"github.com/lorem-dev/duolog/internal/telemetry"
	"github.com/spf13/cobra"
	"go.yaml.in/yaml/v3"
)

// errWorkerTelemetry refuses a worker before any file is read.
var errWorkerTelemetry = errors.New("only the person may run duolog telemetry; a worker cannot")

// modelMarkers are the variables Claude Code and Codex set in the processes
// their tool calls start. Any of them means a model may read the output.
var modelMarkers = []string{"CLAUDECODE", "CLAUDE_CODE_ENTRYPOINT", "CODEX_SANDBOX", "CODEX_SANDBOX_NETWORK_DISABLED", "CODEX_THREAD_ID"}

// isTerminal reports whether v is a file open on a terminal; tests replace it.
var isTerminal = func(v any) bool {
	f, ok := v.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

// modelContext reports whether a model may read this command's output: a
// worker, a lead's tool call, or output that does not go to a terminal. No
// flag can claim the person's terminal.
func modelContext(cmd *cobra.Command) bool {
	if isWorker() {
		return true
	}
	for _, name := range modelMarkers {
		if os.Getenv(name) != "" {
			return true
		}
	}
	return !isTerminal(cmd.OutOrStdout())
}

// newTelemetryCommand holds the person's controls of local telemetry. Every
// subcommand refuses a worker before it reads anything; a lead model's call
// passes the lead hook, which asks the person (Claude) or points to the
// person's terminal (Codex). There is no export and no root override.
func newTelemetryCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "telemetry",
		Short: "Show and manage local telemetry, which never leaves this machine; for the person only",
		Args:  cobra.NoArgs,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			if isWorker() {
				return errWorkerTelemetry
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error { return cmd.Help() },
	}
	cmd.AddCommand(newTelemetryStatusCommand(), newTelemetryShowCommand(), newTelemetryPruneCommand(), newTelemetryPurgeCommand(), newTelemetrySwitchCommand(true), newTelemetrySwitchCommand(false))
	return cmd
}

// openPersonStore opens this project's store for a person's command. With
// collection off it creates nothing and opens an existing root for reading
// and maintenance only; the callers prune first, since turning collection
// off never stops pruning.
func openPersonStore() (*telemetry.Store, error) {
	root, err := projectRoot()
	if err != nil {
		return nil, err
	}
	cfg, err := config.Load(root)
	if err != nil {
		return nil, err
	}
	s, err := telemetry.Open(telemetryOptions(cfg, root))
	if err != nil {
		return nil, err
	}
	return s, nil
}

func newTelemetryStatusCommand() *cobra.Command {
	return &cobra.Command{Use: "status", Short: "Show the switches, the root, what is stored and the protection checks", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		s, err := openPersonStore()
		if err != nil {
			return err
		}
		defer s.Close()
		_, _ = s.Prune(false)
		st, statusErr := s.Status()
		onOff := map[bool]string{true: "on", false: "off"}
		yesNo := map[bool]string{true: "yes", false: "no"}
		id, oldest, last := orNone(st.ProjectID), orNone(st.OldestDay), "never"
		if !st.LastPrune.IsZero() {
			last = st.LastPrune.UTC().Format(time.RFC3339)
		}
		p := st.Protection
		_, err = fmt.Fprintf(cmd.OutOrStdout(), "telemetry: %s\nproject events: %s\nroot: %s\nproject id: %s\nsize: %s (%d bytes)\nsegments: %d\noldest day: %s\npartitions: %d\nlast prune: %s\nretention days: %d\nprune every hours: %d\nmax megabytes: %d\ndropped since process start: %d\nfailed since process start: %d\nprotection: mode fixed %s, backup verified %s, index verified %s, refusal %s\n",
			onOff[st.Enabled], onOff[st.ProjectEnabled], st.Root, id, humanBytes(st.Bytes), st.Bytes, st.Segments, oldest, st.Partitions, last,
			st.RetentionDays, st.PruneEveryHours, st.MaxMegabytes, st.Health.Dropped, st.Health.Failures,
			yesNo[p.ModeFixed], yesNo[p.BackupVerified], yesNo[p.IndexVerified], p.Refusal)
		return errors.Join(statusErr, err)
	}}
}

func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}

// sinceValue is a whole number of hours or days.
var sinceValue = regexp.MustCompile(`^([1-9][0-9]{0,3})([hd])$`)

// parseSince reads --since: 1h to 30d, which is the longest retention.
func parseSince(s string) (time.Duration, error) {
	g := sinceValue.FindStringSubmatch(s)
	if g == nil {
		return 0, fmt.Errorf("--since takes hours or days, such as 12h or 7d")
	}
	n, _ := strconv.Atoi(g[1])
	d := time.Duration(n) * time.Hour
	if g[2] == "d" {
		d *= 24
	}
	if d > 30*24*time.Hour {
		return 0, fmt.Errorf("--since reaches at most 30d, the longest retention")
	}
	return d, nil
}

// projectScope reads --project: here or all.
func projectScope(s string) (all bool, err error) {
	switch s {
	case "here":
		return false, nil
	case "all":
		return true, nil
	}
	return false, fmt.Errorf("--project is here or all")
}

func newTelemetryShowCommand() *cobra.Command {
	var since, scope string
	var asJSON bool
	cmd := &cobra.Command{Use: "show", Short: "Show runs per day, agent, role and outcome, the quota curve and balance moves", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		d, err := parseSince(since)
		if err != nil {
			return err
		}
		all, err := projectScope(scope)
		if err != nil {
			return err
		}
		s, err := openPersonStore()
		if err != nil {
			return err
		}
		defer s.Close()
		_, _ = s.Prune(false)
		from := time.Now().Add(-d)
		r, err := s.Report(telemetry.Query{Since: from, AllProjects: all})
		if err != nil {
			return err
		}
		if asJSON {
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			return enc.Encode(r)
		}
		where := "this project"
		if all {
			where = "all projects"
		}
		return writeReport(cmd, r, from, where)
	}}
	cmd.Flags().StringVar(&since, "since", "7d", "How far back, 1h to 30d")
	cmd.Flags().StringVar(&scope, "project", "here", "Projects to show: here or all")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Print the aggregates as JSON")
	return cmd
}

// writeReport prints the report as three tables.
func writeReport(cmd *cobra.Command, r telemetry.Report, from time.Time, where string) error {
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	clock := func(t int64) string { return time.Unix(t, 0).UTC().Format("2006-01-02 15:04") }
	fmt.Fprintf(w, "Runs since %s UTC, %s:\n", from.UTC().Format("2006-01-02 15:04"), where)
	fmt.Fprintln(w, "DAY\tAGENT\tROLE\tOUTCOME\tRUNS\tIN\tCACHED\tOUT\tUSD\tSECS\tASKS\tDENIALS\tUNPRICED\tNO USAGE")
	for _, d := range r.Daily {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%d\t%d\t%d\t%d\t%.4f\t%d\t%d\t%d\t%d\t%d\n", d.Day, d.Agent, d.Role, d.Outcome, d.Runs, d.In, d.Cached, d.Out, float64(d.USDmicro)/1e6, d.Secs, d.Asks, d.Denials, d.Unpriced, d.UsageUnknown)
	}
	fmt.Fprintln(w, "\nQuota snapshots:")
	fmt.Fprintln(w, "TIME\tAGENT\tSOURCE\tPLAN\tWINDOWS")
	for _, q := range r.Quota {
		var windows []string
		for _, win := range q.Quota.Windows {
			windows = append(windows, fmt.Sprintf("%s %s %.1f%% reset in %dm", win.Kind, win.Family, float64(win.UsedBP)/100, win.ResetInMin))
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", clock(q.T), q.Quota.Agent, q.Quota.Src, q.Quota.Plan, strings.Join(windows, "; "))
	}
	fmt.Fprintln(w, "\nBalance moves:")
	fmt.Fprintln(w, "TIME\tROLE\tCLASS\tFROM\tTO\tRULE")
	target := func(t telemetry.Target) string { return t.Agent + "/" + t.Family + "/" + t.Effort }
	for _, m := range r.Moves {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", clock(m.T), m.Route.Role, m.Route.Class, target(m.Route.Incumbent), target(m.Route.Chosen), m.Route.Rule)
	}
	return w.Flush()
}

func newTelemetryPruneCommand() *cobra.Command {
	return &cobra.Command{Use: "prune", Short: "Apply retention, compression and the size cap now", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		s, err := openPersonStore()
		if err != nil {
			return err
		}
		defer s.Close()
		m, err := s.Prune(true)
		if _, werr := fmt.Fprintf(cmd.OutOrStdout(), "pruned: %d deleted, %d compressed, %d skipped, %s freed\n", m.Deleted, m.Compressed, m.Skipped, humanBytes(m.Bytes)); err == nil {
			err = werr
		}
		return err
	}}
}

func newTelemetryPurgeCommand() *cobra.Command {
	var scope string
	var all, yes bool
	cmd := &cobra.Command{Use: "purge", Short: "Delete telemetry history of this project, or with --all all of it and the salt", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		every, err := projectScope(scope)
		if err != nil {
			return err
		}
		if all && cmd.Flags().Changed("project") && !every {
			return fmt.Errorf("--all and --project here contradict each other")
		}
		all = all || every
		if !yes {
			what := "the telemetry history of this project"
			if all {
				what = "all telemetry history and its salt"
			}
			// Only the person's answer at a terminal confirms; a redirected
			// stdin cannot.
			if !isTerminal(cmd.InOrStdin()) {
				return fmt.Errorf("purge deletes %s: confirm in your terminal or pass --yes", what)
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "Delete %s? Type yes to confirm: ", what)
			line, _ := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
			if strings.TrimSpace(line) != "yes" {
				return fmt.Errorf("purge cancelled")
			}
		}
		s, err := openPersonStore()
		if err != nil {
			return err
		}
		defer s.Close()
		_, _ = s.Prune(false)
		if err := s.Purge(all); err != nil {
			return err
		}
		done := "this project's telemetry history"
		if all {
			done = "all telemetry history and the salt"
		}
		_, err = fmt.Fprintf(cmd.OutOrStdout(), "purged %s; quota balance state is kept\n", done)
		return err
	}}
	cmd.Flags().StringVar(&scope, "project", "here", "Projects to purge: here or all")
	cmd.Flags().BoolVar(&all, "all", false, "Purge every project, the account history and the salt")
	cmd.Flags().BoolVar(&yes, "yes", false, "Purge without asking")
	return cmd
}

func newTelemetrySwitchCommand(enabled bool) *cobra.Command {
	use, short := "off", "Stop collecting for this user; pruning goes on until the data is gone"
	if enabled {
		use, short = "on", "Collect again for this user"
	}
	return &cobra.Command{Use: use, Short: short, Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		if err := writeTelemetryEnabled(enabled); err != nil {
			return err
		}
		line := "telemetry: off in the user config; stored data is pruned as it expires, and duolog telemetry purge deletes it now"
		if enabled {
			line = "telemetry: on in the user config"
			if strings.EqualFold(strings.TrimSpace(os.Getenv("DUOLOG_TELEMETRY")), "off") {
				line += "; DUOLOG_TELEMETRY=off keeps it off here"
			}
		}
		_, err := fmt.Fprintln(cmd.OutOrStdout(), line)
		return err
	}}
}

// writeTelemetryEnabled sets telemetry.enabled in the user config, never the
// project's. Every other key and comment stays; the file is replaced in one
// rename, so a reader sees the old file or the new one.
func writeTelemetryEnabled(enabled bool) error {
	if isWorker() {
		return errWorkerTelemetry
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return fmt.Errorf("user config directory: %w", err)
	}
	dir = filepath.Join(dir, "duolog")
	path := filepath.Join(dir, "config.yaml")
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	if doc.Kind == 0 {
		doc.Kind = yaml.DocumentNode
	}
	if len(doc.Content) == 0 {
		doc.Content = []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}}
	}
	if doc.Kind != yaml.DocumentNode || doc.Content[0].Kind != yaml.MappingNode {
		return fmt.Errorf("%s: the user config is not a mapping", path)
	}
	section, err := yamlMapping(doc.Content[0], "telemetry")
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	value := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: strconv.FormatBool(enabled)}
	if v := yamlValue(section, "enabled"); v != nil {
		v.Kind, v.Tag, v.Value, v.Style, v.Content = value.Kind, value.Tag, value.Value, 0, nil
	} else {
		section.Content = append(section.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "enabled"}, value)
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return err
	}
	if err := enc.Close(); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	// A new file in the folder: a fixed name could follow a link planted there.
	tmp, err := os.CreateTemp(dir, ".config.yaml.*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	_, writeErr := tmp.Write(buf.Bytes())
	if err := errors.Join(writeErr, tmp.Close()); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0600); err != nil {
		return err
	}
	return store.Rename(tmp.Name(), path)
}

// yamlValue is the value of key in a mapping node, or nil.
func yamlValue(m *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

// yamlMapping returns the mapping under key, adding it when it is missing or
// empty.
func yamlMapping(m *yaml.Node, key string) (*yaml.Node, error) {
	v := yamlValue(m, key)
	if v == nil {
		v = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, v)
		return v, nil
	}
	if v.Kind == yaml.ScalarNode && v.Tag == "!!null" {
		v.Kind, v.Tag, v.Value, v.Style = yaml.MappingNode, "!!map", "", 0
	}
	if v.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("%s is not a mapping", key)
	}
	return v, nil
}

// telemetryLeadGuard keeps telemetry and balance for the person when a lead
// model calls a tool: a duolog telemetry or duolog balance command by any
// spelling or wrapper, or a read or write that reaches either root. It uses
// the worker gate's own checks, which compute the roots and place paths
// without opening any file. A Claude lead's call asks the person; a Codex
// lead's call is denied with a pointer to the person's terminal. The mode
// plays no part, full mode included.
func telemetryLeadGuard(agent string, actions []gate.Action) (gate.Decision, bool) {
	roots, rootsErr := gateProtectedRoots()
	for _, a := range actions {
		if a.Kind != "read" && a.Kind != "write" && a.Kind != "exec" {
			continue
		}
		a.ProtectedRoots = nil
		// Without a place for its paths only the command check applies.
		if rootsErr == nil && (a.Work != "" || a.Cwd != "") {
			a.ProtectedRoots = roots
		}
		if gate.Evaluate(config.Policy{}, gate.LeadRules{}, mode.Full, a).Rule != gate.RuleProtected && !leadStoreCommand(a) {
			continue
		}
		if agent == "codex" {
			return gate.Decision{Kind: gate.Deny, Rule: "person-only telemetry", Reason: "duolog telemetry and balance data are for the person only: run it in your terminal"}, true
		}
		return gate.Decision{Kind: gate.Ask, Rule: "person-only telemetry", Reason: "duolog telemetry and balance data are for the person only; the person confirms this call"}, true
	}
	return gate.Decision{}, false
}

// leadStoreCommand finds duolog telemetry or duolog balance behind duolog's
// own flags, such as --lead L, with the lead guard's own parse; the worker
// gate's check skips those flags too, so each one covers the other.
func leadStoreCommand(a gate.Action) bool {
	if a.Kind != "exec" {
		return false
	}
	var parts []string
	if lists, ok := gate.CommandWordLists(a.Target, a.Shell); ok {
		// A plain line: the words of each command, without the prose of a
		// commit message or the like.
		for _, words := range lists {
			parts = append(parts, strings.Join(words, " "))
		}
	} else {
		var uncertain bool
		parts, uncertain = gate.ShellParts(a.Target, a.Shell)
		if uncertain {
			parts = append(parts, a.Target)
		}
	}
	for _, part := range parts {
		for _, verb := range []string{"telemetry", "balance"} {
			if _, ok := leadCommandArgs(part, verb); ok {
				return true
			}
		}
	}
	return false
}

// doctorGOOS is the platform doctor describes; tests set others.
var doctorGOOS = runtime.GOOS

// telemetryDoctor renders doctor's telemetry lines, each "level: message".
// A model reads only the coarse form: on and healthy, off, or unhealthy.
// The person's terminal gets the full line, the warnings and which backup
// exclusions are verified. Isolation is gate + sandbox only where a Claude
// sandbox the person turned on adds the roots to denyRead; native Windows
// has the gate only. The lost-event counters are this process's.
func telemetryDoctor(s telemetry.Status, modelContext, sandboxActive bool, now time.Time) string {
	warnings, backup := telemetryWarnings(s, now)
	if modelContext {
		switch {
		case !s.Enabled:
			return "info: telemetry: off\n"
		case len(warnings) > 0:
			return "warning: telemetry: unhealthy\n"
		}
		return "info: telemetry: on, healthy\n"
	}
	isolation := "gate only"
	if sandboxActive && doctorGOOS != "windows" {
		isolation = "gate + sandbox"
	}
	state := "off"
	if s.Enabled {
		state = "on"
		if !s.ProjectEnabled {
			state = "on, project events off"
		}
	}
	var b strings.Builder
	if s.Protection.Refusal != "none" {
		fmt.Fprintf(&b, "info: telemetry: %s, local only, isolation %s; not collecting\n", state, isolation)
	} else {
		pruned := "never pruned"
		if !s.LastPrune.IsZero() {
			pruned = "pruned " + ago(now.Sub(s.LastPrune))
		}
		fmt.Fprintf(&b, "info: telemetry: %s, local only, isolation %s; %s in %d segments, %d partitions, oldest %s, %s; %d dropped, %d failed since process start\n",
			state, isolation, humanBytes(s.Bytes), s.Segments, s.Partitions, orNone(s.OldestDay), pruned, s.Health.Dropped, s.Health.Failures)
	}
	for _, line := range append(backup, warnings...) {
		b.WriteString(line + "\n")
	}
	return b.String()
}

// telemetryWarnings returns the warnings that make the store unhealthy, and
// the backup lines, which only describe the platform.
func telemetryWarnings(s telemetry.Status, now time.Time) (warnings, backup []string) {
	p := s.Protection
	refusals := map[string]string{
		"symlink": "root is a symlink", "owner": "root is owned by another user", "git": "root is inside a git work tree",
		"sync": "root is in a cloud-synced folder", "nfs": "root is on NFS, where locks are unreliable",
		"permissions": "root cannot be secured", "other": "root failed its checks",
	}
	if p.Refusal != "none" {
		reason, ok := refusals[p.Refusal]
		if !ok {
			reason = refusals["other"]
		}
		return []string{"warning: telemetry: " + reason + "; collection stays off"}, nil
	}
	if p.ModeFixed {
		warnings = append(warnings, "warning: telemetry: root mode was too open and is now owner only")
	}
	if s.Segments > 0 && (s.LastPrune.IsZero() || now.Sub(s.LastPrune) >= 48*time.Hour) {
		warnings = append(warnings, "warning: telemetry: not pruned for 2 days while duolog ran; run duolog telemetry prune")
	}
	if !s.Enabled {
		return warnings, nil
	}
	switch doctorGOOS {
	case "darwin":
		var verified, unverified []string
		for _, c := range []struct {
			name string
			ok   bool
		}{{"Time Machine exclusion", p.BackupVerified}, {"Spotlight exclusion", p.IndexVerified}} {
			if c.ok {
				verified = append(verified, c.name+" verified")
			} else {
				unverified = append(unverified, c.name+" unverified")
			}
		}
		if len(verified) > 0 {
			backup = append(backup, "info: telemetry backup: "+strings.Join(verified, ", "))
		}
		if len(unverified) > 0 {
			backup = append(backup, "warning: telemetry backup: "+strings.Join(unverified, ", "))
		}
	case "linux":
		if p.BackupVerified {
			backup = append(backup, "info: telemetry backup: CACHEDIR.TAG verified for tools that honour it, not Deja Dup")
		} else {
			backup = append(backup, "warning: telemetry backup: CACHEDIR.TAG unverified")
		}
	case "windows":
		backup = append(backup, "info: telemetry backup: Windows Search and OneDrive exclusions of AppData unverified")
	}
	return warnings, backup
}

func ago(d time.Duration) string {
	switch {
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", max(d/time.Minute, 0))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh ago", d/time.Hour)
	}
	return fmt.Sprintf("%dd ago", d/(24*time.Hour))
}

func humanBytes(n int64) string {
	switch {
	case n < 1<<10:
		return fmt.Sprintf("%d B", n)
	case n < 1<<20:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
}
