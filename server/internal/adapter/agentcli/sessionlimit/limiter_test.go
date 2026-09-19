package sessionlimit

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func acquireAsync(t *testing.T, l *Limiter, ctx context.Context) <-chan func() {
	t.Helper()
	out := make(chan func(), 1)
	go func() {
		release, err := l.Acquire(ctx)
		if err != nil {
			close(out)
			return
		}
		out <- release
	}()
	return out
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not reached")
		}
		time.Sleep(time.Millisecond)
	}
}

func mustGet(t *testing.T, ch <-chan func()) func() {
	t.Helper()
	select {
	case r, ok := <-ch:
		if !ok {
			t.Fatal("acquire failed")
		}
		return r
	case <-time.After(2 * time.Second):
		t.Fatal("acquire did not return")
	}
	return nil
}

func mustBlock(t *testing.T, ch <-chan func()) {
	t.Helper()
	select {
	case <-ch:
		t.Fatal("acquire should still be waiting")
	case <-time.After(30 * time.Millisecond):
	}
}

func TestLimitBoundsActiveSessions(t *testing.T) {
	l := New(2)
	ctx := context.Background()
	r1 := mustGet(t, acquireAsync(t, l, ctx))
	r2 := mustGet(t, acquireAsync(t, l, ctx))
	third := acquireAsync(t, l, ctx)
	mustBlock(t, third)
	if s := l.Stats(); s.Active != 2 || s.Waiting != 1 || s.Limit != 2 {
		t.Fatalf("stats %+v", s)
	}
	r1()
	r3 := mustGet(t, third)
	r1()
	if s := l.Stats(); s.Active != 2 {
		t.Fatalf("a second release call must be a no-op: %+v", s)
	}
	r2()
	r3()
	if s := l.Stats(); s.Active != 0 || s.Waiting != 0 {
		t.Fatalf("stats %+v", s)
	}
}

func TestRaisingTheLimitWakesWaiters(t *testing.T) {
	l := New(1)
	ctx := context.Background()
	r1 := mustGet(t, acquireAsync(t, l, ctx))
	a := acquireAsync(t, l, ctx)
	b := acquireAsync(t, l, ctx)
	waitFor(t, func() bool { return l.Stats().Waiting == 2 })
	l.SetLimit(3)
	ra := mustGet(t, a)
	rb := mustGet(t, b)
	if s := l.Stats(); s.Active != 3 || s.Waiting != 0 {
		t.Fatalf("stats %+v", s)
	}
	r1()
	ra()
	rb()
}

func TestLoweringTheLimitNeverInterruptsAndHoldsNewSessions(t *testing.T) {
	l := New(3)
	ctx := context.Background()
	var releases []func()
	for i := 0; i < 3; i++ {
		releases = append(releases, mustGet(t, acquireAsync(t, l, ctx)))
	}
	l.SetLimit(1)
	if s := l.Stats(); s.Active != 3 {
		t.Fatalf("running sessions must stay: %+v", s)
	}
	next := acquireAsync(t, l, ctx)
	mustBlock(t, next)
	releases[0]()
	mustBlock(t, next)
	releases[1]()
	mustBlock(t, next)
	releases[2]()
	r := mustGet(t, next)
	if s := l.Stats(); s.Active != 1 {
		t.Fatalf("stats %+v", s)
	}
	r()
}

func TestCancelledWaiterLeaksNoSlot(t *testing.T) {
	l := New(1)
	r1 := mustGet(t, acquireAsync(t, l, context.Background()))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := l.Acquire(ctx)
		done <- err
	}()
	waitFor(t, func() bool { return l.Stats().Waiting == 1 })
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("err %v", err)
	}
	if s := l.Stats(); s.Waiting != 0 || s.Active != 1 {
		t.Fatalf("stats %+v", s)
	}
	r1()
	r2 := mustGet(t, acquireAsync(t, l, context.Background()))
	if s := l.Stats(); s.Active != 1 {
		t.Fatalf("stats %+v", s)
	}
	r2()
}

func TestGrantRacingCancellationGivesTheSlotBack(t *testing.T) {
	for i := 0; i < 200; i++ {
		l := New(1)
		r1, _ := l.Acquire(context.Background())
		ctx, cancel := context.WithCancel(context.Background())
		var wg sync.WaitGroup
		wg.Add(1)
		var got func()
		go func() {
			defer wg.Done()
			got, _ = l.Acquire(ctx)
		}()
		waitFor(t, func() bool { return l.Stats().Waiting == 1 })
		go cancel()
		r1()
		wg.Wait()
		if got != nil {
			got()
		}
		if s := l.Stats(); s.Active != 0 || s.Waiting != 0 {
			t.Fatalf("iteration %d leaked: %+v", i, s)
		}
	}
}

func TestWaitersAreServedInOrder(t *testing.T) {
	l := New(1)
	r := mustGet(t, acquireAsync(t, l, context.Background()))
	var order []int
	var mu sync.Mutex
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		i := i
		go func() {
			defer wg.Done()
			release, _ := l.Acquire(context.Background())
			mu.Lock()
			order = append(order, i)
			mu.Unlock()
			release()
		}()
		waitFor(t, func() bool { return l.Stats().Waiting == i+1 })
	}
	r()
	wg.Wait()
	for i, v := range order {
		if v != i {
			t.Fatalf("order %v", order)
		}
	}
}

func TestUnlimited(t *testing.T) {
	l := New(0)
	var releases []func()
	for i := 0; i < 50; i++ {
		releases = append(releases, mustGet(t, acquireAsync(t, l, context.Background())))
	}
	if s := l.Stats(); s.Active != 50 || s.Waiting != 0 {
		t.Fatalf("stats %+v", s)
	}
	for _, r := range releases {
		r()
	}
}

func TestAcquireWithDeadContext(t *testing.T) {
	l := New(1)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := l.Acquire(ctx); err == nil {
		t.Fatal("a cancelled context must not take a slot")
	}
	if s := l.Stats(); s.Active != 0 {
		t.Fatalf("stats %+v", s)
	}
}
