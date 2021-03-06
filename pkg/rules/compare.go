package rules

import (
	"errors"
	"fmt"
	"reflect"
	"strings"

	rulefmt "github.com/cortexproject/cortex/pkg/ruler/legacy_rulefmt"
	"github.com/google/go-cmp/cmp"
	"github.com/mitchellh/colorstring"
	yaml "gopkg.in/yaml.v2"
)

var (
	errNameDiff     = errors.New("rule groups are named differently")
	errIntervalDiff = errors.New("rule groups have different intervals")
	errDiffRuleLen  = errors.New("rule groups have a different number of rules")
)

// NamespaceState is used to denote the difference between the staged namespace
// and active namespace for the cortex tenant
type NamespaceState int

const (
	// Unchanged denotes the active namespace is identical to the staged namespace
	Unchanged NamespaceState = iota
	// Created denotes their is not active namespace for the staged namespace
	Created
	// Updated denotes the active namespace is different than the staged namespace
	Updated
	// Deleted denotes their is no staged namespace for the active namespace
	Deleted
)

// NamespaceChange stores the various changes between a staged set of changes
// and the active rules configs.
type NamespaceChange struct {
	Namespace     string
	State         NamespaceState
	GroupsUpdated []UpdatedRuleGroup
	GroupsCreated []rulefmt.RuleGroup
	GroupsDeleted []rulefmt.RuleGroup
}

// SummarizeChanges returns the number of each type of change in a set of changes
func SummarizeChanges(changes []NamespaceChange) (created, updated, deleted int) {
	// Cycle through the results to determine which types of changes have been made
	for _, change := range changes {
		if len(change.GroupsCreated) > 0 {
			created += len(change.GroupsCreated)
		}
		if len(change.GroupsUpdated) > 0 {
			updated += len(change.GroupsUpdated)
		}
		if len(change.GroupsDeleted) > 0 {
			deleted += len(change.GroupsDeleted)
		}
	}
	return
}

// UpdatedRuleGroup is used to store an change between a rule group
type UpdatedRuleGroup struct {
	New      rulefmt.RuleGroup
	Original rulefmt.RuleGroup
}

// CompareGroups differentiates between two rule groups
func CompareGroups(groupOne, groupTwo rulefmt.RuleGroup) error {
	if groupOne.Name != groupTwo.Name {
		return errNameDiff
	}

	if groupOne.Interval != groupTwo.Interval {
		return errIntervalDiff
	}

	if len(groupOne.Rules) != len(groupTwo.Rules) {
		return errDiffRuleLen
	}

	for i := range groupOne.Rules {
		eq := rulesEqual(&groupOne.Rules[i], &groupTwo.Rules[i])
		if !eq {
			return fmt.Errorf("rule #%v does not match %v != %v", i, groupOne.Rules[i], groupTwo.Rules[i])
		}
	}

	return nil
}

func rulesEqual(a, b *rulefmt.Rule) bool {
	// special option to consider nil == empty for map / slice.
	// Code taken from https://pkg.go.dev/github.com/google/go-cmp/cmp?tab=doc#example-Option-EqualEmpty
	alwaysEqual := cmp.Comparer(func(_, _ interface{}) bool { return true })
	opt := cmp.FilterValues(func(x, y interface{}) bool {
		vx, vy := reflect.ValueOf(x), reflect.ValueOf(y)
		return (vx.IsValid() && vy.IsValid() && vx.Type() == vy.Type()) &&
			(vx.Kind() == reflect.Slice || vx.Kind() == reflect.Map) &&
			(vx.Len() == 0 && vy.Len() == 0)
	}, alwaysEqual)

	return cmp.Equal(a, b, opt)
}

// CompareNamespaces returns the differences between the two provided
// namespaces
func CompareNamespaces(original, new RuleNamespace) NamespaceChange {
	result := NamespaceChange{
		Namespace:     new.Namespace,
		State:         Unchanged,
		GroupsUpdated: []UpdatedRuleGroup{},
		GroupsCreated: []rulefmt.RuleGroup{},
		GroupsDeleted: []rulefmt.RuleGroup{},
	}

	origMap := map[string]rulefmt.RuleGroup{}
	for _, g := range original.Groups {
		origMap[g.Name] = g
	}

	for _, newGroup := range new.Groups {
		origGroup, found := origMap[newGroup.Name]
		if !found {
			result.State = Updated
			result.GroupsCreated = append(result.GroupsCreated, newGroup)
			continue
		}
		diff := CompareGroups(newGroup, origGroup)
		if diff != nil {
			result.State = Updated
			result.GroupsUpdated = append(result.GroupsUpdated, UpdatedRuleGroup{
				Original: origGroup,
				New:      newGroup,
			})
		}
		delete(origMap, newGroup.Name)
	}

	for _, group := range origMap {
		result.State = Updated
		result.GroupsDeleted = append(result.GroupsDeleted, group)
	}

	return result
}

// PrintComparisonResult prints the differences between the staged namespace
// and active namespace
func PrintComparisonResult(results []NamespaceChange, verbose bool) error {
	created, updated, deleted := SummarizeChanges(results)

	// If any changes are detected, print the symbol legend
	if (created + updated + deleted) > 0 {
		fmt.Println("Changes are indicated with the following symbols:")
		if created > 0 {
			colorstring.Println("[green]  +[reset] created") //nolint
		}
		if updated > 0 {
			colorstring.Println("[yellow]  +[reset] updated") //nolint
		}
		if deleted > 0 {
			colorstring.Println("[red]  +[reset] deleted") //nolint
		}
		fmt.Println()
		fmt.Println("The following changes will be made if the provided rule set is synced:")
	} else {
		fmt.Println("no changes detected")
		return nil
	}

	for _, change := range results {
		switch change.State {
		case Created:
			colorstring.Printf("[green]+ Namespace: %v\n", change.Namespace)
			for _, c := range change.GroupsCreated {
				colorstring.Printf("[green]  + Group: %v\n", c.Name)
			}
		case Updated:
			colorstring.Printf("[yellow]~ Namespace: %v\n", change.Namespace)
			for _, c := range change.GroupsCreated {
				colorstring.Printf("[green]  + Group: %v\n", c.Name)
			}

			for _, c := range change.GroupsUpdated {
				colorstring.Printf("[yellow]  ~ Group: %v\n", c.New.Name)

				// Print the full diff of the rules if verbose is set
				if verbose {
					newYaml, _ := yaml.Marshal(c.New)
					separated := strings.Split(string(newYaml), "\n")
					for _, l := range separated {
						colorstring.Printf("[green]+ %v\n", l)
					}

					oldYaml, _ := yaml.Marshal(c.Original)
					separated = strings.Split(string(oldYaml), "\n")
					for _, l := range separated {
						colorstring.Printf("[red]+ %v\n", l)
					}
				}
			}

			for _, c := range change.GroupsDeleted {
				colorstring.Printf("[red]  - Group: %v\n", c.Name)
			}
		case Deleted:
			colorstring.Printf("[red]- Namespace: %v\n", change.Namespace)
			for _, c := range change.GroupsDeleted {
				colorstring.Printf("[red]  - Group: %v\n", c.Name)
			}
		}
	}

	fmt.Println()
	fmt.Printf("Diff Summary: %v Groups Created, %v Groups Updated, %v Groups Deleted\n", created, updated, deleted)
	return nil
}
