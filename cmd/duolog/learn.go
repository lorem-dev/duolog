package main

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/lorem-dev/duolog/internal/config"
	"github.com/lorem-dev/duolog/internal/journal"
	"github.com/lorem-dev/duolog/internal/learning"
	"github.com/lorem-dev/duolog/internal/store"
	"github.com/spf13/cobra"
)

func newLearnCommand() *cobra.Command {
	var apply bool
	var applyClass, declineClass string
	cmd := &cobra.Command{Use: "learn", Short: "Propose a learned model order from the journal", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		if (apply || cmd.Flags().Changed("apply-class") || cmd.Flags().Changed("decline-class")) && isWorker() {
			return fmt.Errorf("workers cannot change the learned order")
		}
		root, err := projectRoot()
		if err != nil {
			return err
		}
		cfg, err := config.Load(root)
		if err != nil {
			return err
		}
		// Reading an empty journal must not create state, especially for workers.
		j := &journal.Journal{Dir: filepath.Join(root, ".duolog", "journal")}
		ds, err := journal.ReadAll[journal.Delegation](j, "delegations.ndjson")
		if err != nil {
			return err
		}
		ts, err := journal.ReadAll[journal.Task](j, "tasks.ndjson")
		if err != nil {
			return err
		}
		vs, err := journal.ReadAll[journal.Verdict](j, "verdicts.ndjson")
		if err != nil {
			return err
		}
		declined, err := declinedClasses(j)
		if err != nil {
			return err
		}
		classes := learning.ProposeClasses(cfg, ds, declined)
		if cmd.Flags().Changed("apply-class") || cmd.Flags().Changed("decline-class") {
			name := applyClass
			if cmd.Flags().Changed("decline-class") {
				name = declineClass
			}
			matches := slices.DeleteFunc(slices.Clone(classes), func(p learning.ClassProposal) bool { return p.Name != name })
			if len(matches) != 1 {
				return fmt.Errorf("class proposal %q must identify one current proposal", name)
			}
			if cmd.Flags().Changed("decline-class") {
				s, err := store.Open(root)
				if err != nil {
					return err
				}
				j, err = journal.Open(s.Dir)
				if err != nil {
					return err
				}
				return j.Append("declined.ndjson", classDecline{Declined: journal.Declined{Time: time.Now().UTC(), Name: name}, Group: matches[0].DeclineKey()})
			}
			_, _, err := learning.ApplyClass(root, cfg, matches[0])
			return err
		}
		if err := learnOrder(cmd, root, cfg, ds, ts, vs, apply); err != nil {
			return err
		}
		for _, p := range classes {
			old, updated, err := previewClass(root, cfg, p)
			if err != nil {
				return err
			}
			if _, err := fmt.Fprintf(cmd.OutOrStdout(), "class proposal %s:\nevidence: %s\nproposed .duolog/config.yaml:\n%sapply with: duolog learn --apply-class %s; decline with: duolog learn --decline-class %s\n", p.Name, p.Evidence, learning.LineDiff(old, updated), p.Name, p.Name); err != nil {
				return err
			}
		}
		return nil
	}}
	cmd.Flags().BoolVar(&apply, "apply", false, "Write the proposed learned order")
	cmd.Flags().StringVar(&applyClass, "apply-class", "", "Apply the named class or classification rule proposal")
	cmd.Flags().StringVar(&declineClass, "decline-class", "", "Stop offering the named class or rule proposal")
	cmd.MarkFlagsMutuallyExclusive("apply", "apply-class", "decline-class")
	return cmd
}

func learnOrder(cmd *cobra.Command, root string, cfg *config.Config, ds []journal.Delegation, ts []journal.Task, vs []journal.Verdict, apply bool) error {
	st := learning.Aggregate(cfg, ds, ts, vs)
	p := learning.Propose(cfg, st, vs)
	if !learning.Changed(cfg, p) {
		_, err := fmt.Fprintln(cmd.OutOrStdout(), "no change to the learned order")
		return err
	}
	path := filepath.Join(root, ".duolog", config.LearnedRoutingFile)
	old, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	data, err := learning.Render(p)
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	if _, err := fmt.Fprintf(out, "proposed .duolog/routing.learned.yaml:\n%sevidence:\n", learning.LineDiff(old, data)); err != nil {
		return err
	}
	var keys []string
	for key, v := range p.Ladders {
		one := config.LearnedRouting{Ladders: map[string]config.LearnedLadder{key: v}}
		if learning.Changed(cfg, one) {
			keys = append(keys, key)
		}
	}
	slices.Sort(keys)
	for _, key := range keys {
		for _, line := range learning.Evidence(st, key) {
			if _, err := fmt.Fprintln(out, line); err != nil {
				return err
			}
		}
	}
	if !apply {
		_, err := fmt.Fprintln(out, "apply with: duolog learn --apply")
		return err
	}
	if _, err := store.Open(root); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}

// Group is optional so older name-only records keep their original meaning.
// New decisions retain the public name while declining only the offered group.
type classDecline struct {
	journal.Declined
	Group string `json:"group,omitempty"`
}

func declinedClasses(j *journal.Journal) ([]string, error) {
	records, err := journal.ReadAll[classDecline](j, "declined.ndjson")
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(records))
	for _, r := range records {
		if r.Group != "" {
			names = append(names, r.Group)
		} else {
			names = append(names, r.Name)
		}
	}
	return names, nil
}

// Preview uses a private temporary copy so plain learn, including in a worker,
// never rewrites the project's config even briefly while computing its diff.
func previewClass(root string, cfg *config.Config, p learning.ClassProposal) ([]byte, []byte, error) {
	tmp, err := os.MkdirTemp("", "duolog-class-preview-")
	if err != nil {
		return nil, nil, err
	}
	defer os.RemoveAll(tmp)
	state := filepath.Join(tmp, ".duolog")
	if err := os.Mkdir(state, 0700); err != nil {
		return nil, nil, err
	}
	for _, name := range []string{"config.yaml", "config.local.yaml", config.LearnedFile, config.LearnedRoutingFile} {
		data, err := os.ReadFile(filepath.Join(root, ".duolog", name))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, nil, err
		}
		if err := os.WriteFile(filepath.Join(state, name), data, 0600); err != nil {
			return nil, nil, err
		}
	}
	return learning.ApplyClass(tmp, cfg, p)
}
