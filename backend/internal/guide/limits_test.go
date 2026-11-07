package guide

import (
	"testing"
	"time"
)

func TestLimitsRollingWindowAndInferenceSlots(t *testing.T) {
	l := NewLimits(10, 4)
	now := time.Unix(100000, 0)
	for i := range 10 {
		if err := l.allowAt("one", now.Add(time.Duration(i)*time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	if l.allowAt("one", now.Add(59999*time.Millisecond)) == nil {
		t.Fatal("eleventh request allowed within rolling minute")
	}
	if err := l.allowAt("two", now); err != nil {
		t.Fatal("another IP consumed first budget")
	}
	if err := l.allowAt("one", now.Add(time.Minute)); err != nil {
		t.Fatal("expired first request not released")
	}
	var release []func()
	for range 4 {
		r, err := l.Acquire()
		if err != nil {
			t.Fatal(err)
		}
		release = append(release, r)
	}
	if _, err := l.Acquire(); err == nil {
		t.Fatal("fifth inference must not queue")
	}
	release[0]()
	r, err := l.Acquire()
	if err != nil {
		t.Fatal("cancellation slot not reusable")
	}
	r()
	for _, r := range release[1:] {
		r()
	}
	if err := l.allowAt("later", now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, exists := l.byIP["two"]; exists {
		t.Fatal("expired source retained")
	}
}
