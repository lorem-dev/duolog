package main

import (
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/lorem-dev/duolog/internal/journal"
	"github.com/spf13/cobra"
)

func newVerdictCommand() *cobra.Command {
	var note string
	cmd := &cobra.Command{Use: "verdict <id> confirm|correct [criterion=o/p ...]", Short: "Record the person's confirmation or correction of a judge verdict", Args: cobra.MinimumNArgs(2), RunE: func(cmd *cobra.Command, args []string) error {
		if isWorker() {
			return fmt.Errorf("workers cannot change the journal")
		}
		action := args[1]
		if action != "confirm" && action != "correct" {
			return fmt.Errorf("verdict decision must be confirm or correct")
		}
		if action == "confirm" && len(args) != 2 {
			return fmt.Errorf("confirm does not take scores")
		}
		if action == "correct" && len(args) < 3 {
			return fmt.Errorf("correct needs at least one criterion=original/profile score")
		}
		root, err := projectRoot()
		if err != nil {
			return err
		}
		j := &journal.Journal{Dir: filepath.Join(root, ".duolog", "journal")}
		vs, err := journal.ReadAll[journal.Verdict](j, "verdicts.ndjson")
		if err != nil {
			return err
		}
		i := slices.IndexFunc(vs, func(v journal.Verdict) bool { return v.Kind == "verdict" && v.ID == args[0] })
		if i < 0 {
			return fmt.Errorf("verdict %s is not in the journal", args[0])
		}
		decision := journal.Verdict{Kind: "decision", Time: time.Now().UTC(), ID: args[0], Decision: action, Note: note}
		seen := map[string]bool{}
		for _, arg := range args[2:] {
			equal := strings.LastIndexByte(arg, '=')
			if equal < 1 {
				return fmt.Errorf("correction %q: want criterion=original/profile", arg)
			}
			criterion := arg[:equal]
			if !slices.ContainsFunc(vs[i].Scores, func(s journal.Score) bool { return s.Criterion == criterion }) {
				return fmt.Errorf("unknown criterion %q", criterion)
			}
			if seen[criterion] {
				return fmt.Errorf("criterion %q corrected more than once", criterion)
			}
			parts := strings.Split(arg[equal+1:], "/")
			if len(parts) != 2 {
				return fmt.Errorf("criterion %q: want original/profile scores 0..3", criterion)
			}
			o, oe := strconv.Atoi(parts[0])
			p, pe := strconv.Atoi(parts[1])
			if oe != nil || pe != nil || o < 0 || o > 3 || p < 0 || p > 3 {
				return fmt.Errorf("criterion %q needs original/profile scores 0..3", criterion)
			}
			seen[criterion] = true
			decision.Scores = append(decision.Scores, journal.Score{Criterion: criterion, Original: o, Profile: p})
		}
		return j.Append("verdicts.ndjson", decision)
	}}
	cmd.Flags().StringVar(&note, "note", "", "One-line note about the decision")
	return cmd
}
