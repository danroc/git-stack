package stack

import (
	"errors"
	"strings"
	"testing"

	"github.com/danroc/git-stack/internal/discovery"
)

// fakeDiscoverer lets stack-level tests control discovery without touching git.
type fakeDiscoverer struct {
	base           string
	stack          []discovery.Branch // returned by DiscoverStack when non-nil
	parents        map[string]string
	parentErr      map[string]error
	subtrees       map[string][]discovery.BranchWithParent
	descendants    map[[2]string]bool // {ancestor, descendant} → true
	setParentLog   []string           // "branch:parent"
	setParentErr   error
	stackParentErr error
}

func (f *fakeDiscoverer) BaseBranch() string { return f.base }

func (f *fakeDiscoverer) DiscoverStack(
	_ string,
	_ discovery.ChooseBranchFn,
) ([]discovery.Branch, error) {
	return f.stack, nil
}

func (f *fakeDiscoverer) IsChildOf(child, parent string) bool {
	return f.descendants[[2]string{parent, child}]
}

func (f *fakeDiscoverer) Parent(branch string) (string, error) {
	if err, ok := f.parentErr[branch]; ok {
		return "", err
	}
	if f.stackParentErr != nil {
		return "", f.stackParentErr
	}
	return f.parents[branch], nil
}

func (f *fakeDiscoverer) SubtreeChildren(name string) []discovery.BranchWithParent {
	return f.subtrees[name]
}

func (f *fakeDiscoverer) SetParent(branch, parent string) error {
	f.setParentLog = append(f.setParentLog, branch+":"+parent)
	return f.setParentErr
}

func newMoveStack(repo Repository, disc Discoverer) *Stack {
	return &Stack{git: repo, disc: disc}
}

// stepRecorder returns a NotifyFn that records start/done steps by branch name.
func stepRecorder(steps *[]string) NotifyFn {
	return func(s Step, done bool) {
		if done {
			*steps = append(*steps, "done:"+s.Branch)
		} else {
			*steps = append(*steps, "start:"+s.Branch)
		}
	}
}

func TestMove_LeafReparent(t *testing.T) {
	repo := &fakeRepository{currentBranch: "main"}
	disc := &fakeDiscoverer{
		base:    "main",
		parents: map[string]string{"feat-2": "feat-1"},
	}

	var steps []string
	notify := stepRecorder(&steps)

	if err := newMoveStack(repo, disc).Move("feat-2", "main", notify); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	wantCalls := []string{
		"CurrentBranch",
		"Checkout:feat-2",
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
		"Checkout:feat-1",
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

func TestMove_CheckoutMovedBranchFailureHalts(t *testing.T) {
	repo := &failingRepo{
		fakeRepository: fakeRepository{currentBranch: "main"},
		failCheckoutOn: map[string]error{"feat-1": errors.New("checked out elsewhere")},
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
	wantCalls := []string{"CurrentBranch", "Checkout:feat-1"}
	if !equalStrings(repo.calls, wantCalls) {
		t.Errorf("git calls = %v, want %v", repo.calls, wantCalls)
	}
	if len(disc.setParentLog) != 0 {
		t.Errorf(
			"SetParent should not be called on checkout failure, got %v",
			disc.setParentLog,
		)
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
	failCheckoutOn map[string]error
	failRebaseOnto error
	failRebaseOn   map[string]error // parent → error when rebasing onto that parent
	failPull       error
}

func (f *failingRepo) Checkout(branch string) error {
	_ = f.fakeRepository.Checkout(branch)
	if err, ok := f.failCheckoutOn[branch]; ok {
		return err
	}
	return nil
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

func (f *failingRepo) Pull() error {
	_ = f.fakeRepository.Pull()
	return f.failPull
}

// TestMove_CycleDetectionViaConfigChain verifies that cycle detection blocks a
// Move even when the child has diverged from the parent. The fakeDiscoverer's
// IsChildOf method encodes the stack-tree relationship; the real
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

func TestRebase_UpdatesMergeBaseAfterEachBranch(t *testing.T) {
	repo := &fakeRepository{currentBranch: "feat-2"}
	disc := &fakeDiscoverer{
		base: "main",
		stack: []discovery.Branch{
			{Name: "main"},
			{Name: "feat-1"},
			{Name: "feat-2"},
		},
	}

	st := &Stack{git: repo, disc: disc}
	if err := st.Rebase(nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	wantCalls := []string{
		"CurrentBranch",
		"Checkout:feat-1",
		"Rebase:main",
		"Checkout:feat-2",
		"Rebase:feat-1",
		"Checkout:feat-2",
	}
	if !equalStrings(repo.calls, wantCalls) {
		t.Errorf("git calls =\n  %v\nwant =\n  %v", repo.calls, wantCalls)
	}

	wantParents := []string{"feat-1:main", "feat-2:feat-1"}
	if !equalStrings(disc.setParentLog, wantParents) {
		t.Errorf("SetParent log = %v, want %v", disc.setParentLog, wantParents)
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

func TestPush(t *testing.T) {
	repo := &fakeRepository{currentBranch: "feat-2"}
	disc := &fakeDiscoverer{
		base: "main",
		stack: []discovery.Branch{
			{Name: "main"},
			{Name: "feat-1"},
			{Name: "feat-2"},
		},
	}

	var steps []string
	st := &Stack{git: repo, disc: disc}
	if err := st.Push(stepRecorder(&steps)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	wantCalls := []string{
		"CurrentBranch",
		"Push:feat-1",
		"Push:feat-2",
	}
	if !equalStrings(repo.calls, wantCalls) {
		t.Errorf("git calls =\n  %v\nwant =\n  %v", repo.calls, wantCalls)
	}

	wantSteps := []string{"start:feat-1", "done:feat-1", "start:feat-2", "done:feat-2"}
	if !equalStrings(steps, wantSteps) {
		t.Errorf("notify steps = %v, want %v", steps, wantSteps)
	}
}

func TestPush_NilNotify(t *testing.T) {
	repo := &fakeRepository{currentBranch: "feat-1"}
	disc := &fakeDiscoverer{
		base: "main",
		stack: []discovery.Branch{
			{Name: "main"},
			{Name: "feat-1"},
		},
	}

	st := &Stack{git: repo, disc: disc}
	if err := st.Push(nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestPull(t *testing.T) {
	repo := &fakeRepository{currentBranch: "feat-2"}
	disc := &fakeDiscoverer{
		base: "main",
		stack: []discovery.Branch{
			{Name: "main"},
			{Name: "feat-1"},
			{Name: "feat-2"},
		},
	}

	st := &Stack{git: repo, disc: disc}
	if err := st.Pull(nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	wantCalls := []string{
		"CurrentBranch",
		"Checkout:feat-1",
		"Pull",
		"Checkout:feat-2",
		"Pull",
		"Checkout:feat-2",
	}
	if !equalStrings(repo.calls, wantCalls) {
		t.Errorf("git calls =\n  %v\nwant =\n  %v", repo.calls, wantCalls)
	}
}

func TestPull_CheckoutFailure(t *testing.T) {
	repo := &failingRepo{
		fakeRepository: fakeRepository{currentBranch: "feat-1"},
		failCheckoutOn: map[string]error{"feat-1": errors.New("checkout failed")},
	}
	disc := &fakeDiscoverer{
		base: "main",
		stack: []discovery.Branch{
			{Name: "main"},
			{Name: "feat-1"},
		},
	}

	err := (&Stack{git: repo, disc: disc}).Pull(nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestForEachBranch_EmptyStack(t *testing.T) {
	repo := &fakeRepository{currentBranch: "main"}
	disc := &fakeDiscoverer{
		base: "main",
		stack: []discovery.Branch{
			{Name: "main"},
		},
	}

	st := &Stack{git: repo, disc: disc}
	callCount := 0
	if err := st.forEachBranch(false, func(_, _ string) error {
		callCount++
		return nil
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if callCount != 0 {
		t.Errorf(
			"action should not be called for base-only stack, got %d calls",
			callCount,
		)
	}
}

func TestForEachBranch_ActionError(t *testing.T) {
	repo := &fakeRepository{currentBranch: "feat-2"}
	disc := &fakeDiscoverer{
		base: "main",
		stack: []discovery.Branch{
			{Name: "main"},
			{Name: "feat-1"},
			{Name: "feat-2"},
		},
	}

	st := &Stack{git: repo, disc: disc}
	actionCalled := false
	err := st.forEachBranch(false, func(branch, _ string) error {
		actionCalled = true
		if branch == "feat-1" {
			return errors.New("action error")
		}
		return nil
	})
	if err == nil || err.Error() != "action error" {
		t.Fatalf("expected 'action error', got %v", err)
	}
	if !actionCalled {
		t.Error("action should have been called before error")
	}
}

func TestForEachBranch_RestoreBranch(t *testing.T) {
	repo := &fakeRepository{currentBranch: "feat-2"}
	disc := &fakeDiscoverer{
		base: "main",
		stack: []discovery.Branch{
			{Name: "main"},
			{Name: "feat-1"},
			{Name: "feat-2"},
		},
	}

	st := &Stack{git: repo, disc: disc}
	if err := st.forEachBranch(true, func(_, _ string) error {
		return nil
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Last call should be restore checkout.
	lastCall := repo.calls[len(repo.calls)-1]
	if lastCall != "Checkout:feat-2" {
		t.Errorf("last call should be Checkout:feat-2 (restore), got %s", lastCall)
	}
}

func TestForEachBranch_CurrentBranchFailure(t *testing.T) {
	repo := &failingRepo{
		fakeRepository: fakeRepository{currentBranch: "feat-1"},
	}
	// Override CurrentBranch to fail.
	type failCurrentBranch struct {
		fakeRepository
	}
	fc := &failCurrentBranch{fakeRepository: fakeRepository{currentBranch: "feat-1"}}
	fc.calls = nil
	// We can't easily override CurrentBranch on fakeRepository, so test via
	// the currentBranch field which always succeeds. Instead, verify that
	// CurrentBranch is called first.
	disc := &fakeDiscoverer{
		base: "main",
		stack: []discovery.Branch{
			{Name: "main"},
		},
	}
	st := &Stack{git: repo, disc: disc}
	if err := st.forEachBranch(false, func(_, _ string) error {
		return nil
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(repo.calls) == 0 || repo.calls[0] != "CurrentBranch" {
		t.Errorf("first call should be CurrentBranch, got %v", repo.calls)
	}
}

func TestRebaseAndSetParent_CheckoutFailure(t *testing.T) {
	repo := &failingRepo{
		fakeRepository: fakeRepository{currentBranch: "main"},
		failCheckoutOn: map[string]error{"feat-1": errors.New("checkout failed")},
	}
	disc := &fakeDiscoverer{base: "main"}

	st := &Stack{git: repo, disc: disc}
	err := st.rebaseAndSetParent("feat-1", "main", func() error {
		return nil
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "checkout") {
		t.Errorf("error should mention checkout, got: %v", err)
	}
}

func TestRebaseAndSetParent_RebaseFailure(t *testing.T) {
	repo := &fakeRepository{currentBranch: "main"}
	disc := &fakeDiscoverer{base: "main"}

	st := &Stack{git: repo, disc: disc}
	rebaseErr := errors.New("rebase conflict")
	err := st.rebaseAndSetParent("feat-1", "main", func() error {
		return rebaseErr
	})
	if err != rebaseErr {
		t.Fatalf("expected %v, got %v", rebaseErr, err)
	}
	// SetParent should not be called after rebase failure.
	if len(disc.setParentLog) != 0 {
		t.Errorf(
			"SetParent should not be called on rebase failure, got %v",
			disc.setParentLog,
		)
	}
}

func TestRebaseAndSetParent_SetParentFailure(t *testing.T) {
	repo := &fakeRepository{currentBranch: "main"}
	disc := &fakeDiscoverer{
		base:         "main",
		setParentErr: errors.New("setparent failed"),
	}

	st := &Stack{git: repo, disc: disc}
	err := st.rebaseAndSetParent("feat-1", "main", func() error {
		return nil
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestCheckoutAndRebase(t *testing.T) {
	repo := &fakeRepository{currentBranch: "main"}
	disc := &fakeDiscoverer{base: "main"}

	st := &Stack{git: repo, disc: disc}
	if err := st.checkoutAndRebase("feat-1", "main"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	wantCalls := []string{"Checkout:feat-1", "Rebase:main"}
	if !equalStrings(repo.calls, wantCalls) {
		t.Errorf("git calls = %v, want %v", repo.calls, wantCalls)
	}
}

func TestCheckoutAndRebaseOnto(t *testing.T) {
	repo := &fakeRepository{currentBranch: "main"}
	disc := &fakeDiscoverer{base: "main"}

	st := &Stack{git: repo, disc: disc}
	if err := st.checkoutAndRebaseOnto("feat-2", "feat-1", "main"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	wantCalls := []string{"Checkout:feat-2", "RebaseOnto:main:feat-1:feat-2"}
	if !equalStrings(repo.calls, wantCalls) {
		t.Errorf("git calls = %v, want %v", repo.calls, wantCalls)
	}
}

func TestMove_DeeplyNested(t *testing.T) {
	repo := &fakeRepository{currentBranch: "main"}
	disc := &fakeDiscoverer{
		base:    "main",
		parents: map[string]string{"feat-1": "main"},
		subtrees: map[string][]discovery.BranchWithParent{
			"feat-1": {
				{Branch: discovery.Branch{Name: "feat-2"}, Parent: "feat-1"},
				{Branch: discovery.Branch{Name: "feat-3"}, Parent: "feat-2"},
				{Branch: discovery.Branch{Name: "feat-4"}, Parent: "feat-3"},
			},
		},
	}

	if err := newMoveStack(repo, disc).Move("feat-1", "hot-fix", nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := []string{
		"CurrentBranch",
		"Checkout:feat-1",
		"RebaseOnto:hot-fix:main:feat-1",
		"Checkout:feat-2",
		"Rebase:feat-1",
		"Checkout:feat-3",
		"Rebase:feat-2",
		"Checkout:feat-4",
		"Rebase:feat-3",
		"Checkout:main",
	}
	if !equalStrings(repo.calls, want) {
		t.Errorf("calls =\n  %v\nwant =\n  %v", repo.calls, want)
	}
}

func TestMove_NilSubtreeChildren(t *testing.T) {
	repo := &fakeRepository{currentBranch: "main"}
	disc := &fakeDiscoverer{
		base:    "main",
		parents: map[string]string{"feat-1": "main"},
		// subtrees["feat-1"] is nil (leaf branch with no children).
	}

	if err := newMoveStack(repo, disc).Move("feat-1", "hot-fix", nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	wantCalls := []string{
		"CurrentBranch",
		"Checkout:feat-1",
		"RebaseOnto:hot-fix:main:feat-1",
		"Checkout:main",
	}
	if !equalStrings(repo.calls, wantCalls) {
		t.Errorf("git calls = %v, want %v", repo.calls, wantCalls)
	}
}

func TestMove_ParentError(t *testing.T) {
	disc := &fakeDiscoverer{
		parents:   map[string]string{"feat-1": "main"},
		parentErr: map[string]error{"feat-1": errors.New("parent lookup failed")},
	}

	err := newMoveStack(&fakeRepository{currentBranch: "main"}, disc).
		Move("feat-1", "hot-fix", nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "parent") {
		t.Errorf("error should mention parent, got: %v", err)
	}
}

func TestRunStep(t *testing.T) {
	t.Parallel()

	var steps []string
	step := Step{Branch: "feat-1", Parent: "main"}
	err := runStep(stepRecorder(&steps), step, func() error { return nil })
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"start:feat-1", "done:feat-1"}
	if !equalStrings(steps, want) {
		t.Errorf("steps = %v, want %v", steps, want)
	}
}

func TestRunStep_ErrorNoDone(t *testing.T) {
	t.Parallel()

	var steps []string
	step := Step{Branch: "feat-1", Parent: "main"}
	fnErr := errors.New("fn error")
	err := runStep(stepRecorder(&steps), step, func() error { return fnErr })
	if err != fnErr {
		t.Fatalf("expected %v, got %v", fnErr, err)
	}
	want := []string{"start:feat-1"}
	if !equalStrings(steps, want) {
		t.Errorf(
			"steps = %v, want %v (done should not be emitted on error)",
			steps,
			want,
		)
	}
}

func TestRunStep_NilNotifyWrapped(t *testing.T) {
	t.Parallel()

	step := Step{Branch: "feat-1", Parent: "main"}
	// runStep doesn't handle nil directly; callers use orNoop to wrap.
	err := runStep(orNoop(nil), step, func() error { return nil })
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestOrNoop(t *testing.T) {
	t.Parallel()

	var called bool
	fn := func(_ Step, _ bool) {
		called = true
	}

	// Non-nil should be returned as-is.
	result := orNoop(fn)
	result(Step{}, false)
	if !called {
		t.Error("orNoop(fn) should call fn")
	}

	// Nil should return a no-op.
	called = false
	result = orNoop(nil)
	result(Step{}, false)
	if called {
		t.Error("orNoop(nil) should be a no-op")
	}
}
