package stack

import (
	"errors"
	"strings"
	"testing"

	"git-stack/pkg/discovery"
)

// fakeDiscoverer lets stack-level tests control discovery without touching git.
type fakeDiscoverer struct {
	base         string
	parents      map[string]string
	parentErr    map[string]error
	subtrees     map[string][]discovery.BranchWithParent
	descendants  map[[2]string]bool // {ancestor, descendant} → true
	setParentLog []string           // "branch:parent"
}

func (f *fakeDiscoverer) BaseBranch() string { return f.base }

func (f *fakeDiscoverer) DiscoverStack(
	_ string,
	_ discovery.ChooseBranchFn,
) ([]discovery.Branch, error) {
	return nil, nil
}

func (f *fakeDiscoverer) IsBranchDescendant(ancestor, descendant string) bool {
	return f.descendants[[2]string{ancestor, descendant}]
}

func (f *fakeDiscoverer) Parent(branch string) (string, error) {
	if err, ok := f.parentErr[branch]; ok {
		return "", err
	}
	return f.parents[branch], nil
}

func (f *fakeDiscoverer) SubtreeMembers(name string) []discovery.BranchWithParent {
	return f.subtrees[name]
}

func (f *fakeDiscoverer) SetParent(branch, parent string) error {
	f.setParentLog = append(f.setParentLog, branch+":"+parent)
	return nil
}

func newMoveStack(repo Repository, disc Discoverer) *Stack {
	return &Stack{git: repo, disc: disc}
}

func TestMove_LeafReparent(t *testing.T) {
	repo := &fakeRepository{currentBranch: "main"}
	disc := &fakeDiscoverer{
		base:    "main",
		parents: map[string]string{"feat-2": "feat-1"},
	}

	var steps []string
	notify := func(s Step, done bool) {
		if done {
			steps = append(steps, "done:"+s.Branch)
		} else {
			steps = append(steps, "start:"+s.Branch)
		}
	}

	if err := newMoveStack(repo, disc).Move("feat-2", "main", notify); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	wantCalls := []string{
		"CurrentBranch",
		"RebaseOnto:main:feat-1:feat-2",
		"Checkout:main",
	}
	if !equalStrings(repo.calls, wantCalls) {
		t.Errorf("git calls = %v, want %v", repo.calls, wantCalls)
	}
	if len(disc.setParentLog) != 1 || disc.setParentLog[0] != "feat-2:main" {
		t.Errorf("SetParent log = %v, want [feat-2:main]", disc.setParentLog)
	}
	wantSteps := []string{"start:feat-2", "done:feat-2"}
	if !equalStrings(steps, wantSteps) {
		t.Errorf("notify steps = %v, want %v", steps, wantSteps)
	}
}

func TestMove_CascadesDescendants(t *testing.T) {
	repo := &fakeRepository{currentBranch: "main"}
	disc := &fakeDiscoverer{
		base:    "main",
		parents: map[string]string{"feat-1": "main"},
		subtrees: map[string][]discovery.BranchWithParent{
			"feat-1": {
				{Branch: discovery.Branch{Name: "feat-2"}, Parent: "feat-1"},
				{Branch: discovery.Branch{Name: "feat-3"}, Parent: "feat-2"},
			},
		},
	}

	if err := newMoveStack(repo, disc).Move("feat-1", "hot-fix", nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := []string{
		"CurrentBranch",
		"RebaseOnto:hot-fix:main:feat-1",
		"Checkout:feat-2",
		"Rebase:feat-1",
		"Checkout:feat-3",
		"Rebase:feat-2",
		"Checkout:main",
	}
	if !equalStrings(repo.calls, want) {
		t.Errorf("calls =\n  %v\nwant =\n  %v", repo.calls, want)
	}
}

func TestMove_RejectsSelf(t *testing.T) {
	err := newMoveStack(&fakeRepository{currentBranch: "main"}, &fakeDiscoverer{}).
		Move("feat-1", "feat-1", nil)
	if err == nil || !strings.Contains(err.Error(), "itself") {
		t.Fatalf("got %v, want error about moving onto itself", err)
	}
}

func TestMove_RejectsCycle(t *testing.T) {
	disc := &fakeDiscoverer{
		descendants: map[[2]string]bool{{"feat-1", "feat-2"}: true},
	}
	err := newMoveStack(&fakeRepository{currentBranch: "main"}, disc).
		Move("feat-1", "feat-2", nil)
	if err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("got %v, want cycle error", err)
	}
}

func TestMove_RejectsSameParent(t *testing.T) {
	disc := &fakeDiscoverer{
		parents: map[string]string{"feat-1": "main"},
	}
	err := newMoveStack(&fakeRepository{currentBranch: "main"}, disc).
		Move("feat-1", "main", nil)
	if err == nil || !strings.Contains(err.Error(), "already a child") {
		t.Fatalf("got %v, want already-a-child error", err)
	}
}

func TestMove_RebaseOntoFailureHalts(t *testing.T) {
	repo := &failingRepo{
		fakeRepository: fakeRepository{currentBranch: "main"},
		failRebaseOnto: errors.New("conflict"),
	}
	disc := &fakeDiscoverer{
		parents: map[string]string{"feat-1": "main"},
		subtrees: map[string][]discovery.BranchWithParent{
			"feat-1": {{Branch: discovery.Branch{Name: "feat-2"}, Parent: "feat-1"}},
		},
	}

	if err := newMoveStack(repo, disc).Move("feat-1", "hot-fix", nil); err == nil {
		t.Fatal("expected error, got nil")
	}
	// No cascade, no SetParent, no restore.
	for _, c := range repo.calls {
		if c == "Checkout:feat-2" || c == "Rebase:feat-1" || c == "Checkout:main" {
			t.Errorf("unexpected call after RebaseOnto failure: %s", c)
		}
	}
	if len(disc.setParentLog) != 0 {
		t.Errorf("SetParent should not be called on failure, got %v", disc.setParentLog)
	}
}

func TestMove_DescendantRebaseFailureHalts(t *testing.T) {
	repo := &failingRepo{
		fakeRepository: fakeRepository{currentBranch: "main"},
		failRebaseOn:   map[string]error{"feat-1": errors.New("conflict")},
	}
	disc := &fakeDiscoverer{
		parents: map[string]string{"feat-1": "main"},
		subtrees: map[string][]discovery.BranchWithParent{
			"feat-1": {
				{Branch: discovery.Branch{Name: "feat-2"}, Parent: "feat-1"},
				{Branch: discovery.Branch{Name: "feat-3"}, Parent: "feat-2"},
			},
		},
	}

	if err := newMoveStack(repo, disc).Move("feat-1", "hot-fix", nil); err == nil {
		t.Fatal("expected error, got nil")
	}
	// feat-3 and restore must not run after feat-2's rebase failure.
	for _, c := range repo.calls {
		if c == "Checkout:feat-3" || c == "Rebase:feat-2" || c == "Checkout:main" {
			t.Errorf("unexpected call after cascade failure: %s", c)
		}
	}
}

// failingRepo extends fakeRepository with configurable errors.
type failingRepo struct {
	fakeRepository
	failRebaseOnto error
	failRebaseOn   map[string]error // parent → error when rebasing onto that parent
}

func (f *failingRepo) RebaseOnto(newBase, upstream, branch string) error {
	_ = f.fakeRepository.RebaseOnto(newBase, upstream, branch)
	return f.failRebaseOnto
}

func (f *failingRepo) Rebase(onto string) error {
	_ = f.fakeRepository.Rebase(onto)
	if err, ok := f.failRebaseOn[onto]; ok {
		return err
	}
	return nil
}

// TestMove_CycleDetectionViaConfigChain verifies that cycle detection blocks a
// Move even when the child has diverged from the parent. The fakeDiscoverer's
// IsBranchDescendant method encodes the stack-tree relationship; the real
// Engine implementation (Task 11) uses the config chain for the same purpose.
func TestMove_CycleDetectionViaConfigChain(t *testing.T) {
	disc := &fakeDiscoverer{
		descendants: map[[2]string]bool{
			{"feat-1", "feat-2"}: true, // feat-2 is a descendant of feat-1
		},
	}
	err := newMoveStack(&fakeRepository{currentBranch: "main"}, disc).
		Move("feat-1", "feat-2", nil)
	if err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Errorf("Move must fail with cycle error, got: %v", err)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
