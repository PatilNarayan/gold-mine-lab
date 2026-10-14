package strategy

import "testing"

func TestRegistry(t *testing.T) {
	r := NewRegistry()
	s := NewEMACross()

	if err := r.Register(s); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := r.Register(NewEMACross()); err == nil {
		t.Fatal("duplicate ID must fail")
	}
	if got, ok := r.Get("ema-cross"); !ok || got != s {
		t.Fatal("Get returned wrong result")
	}
	if _, ok := r.Get("nope"); ok {
		t.Fatal("unknown ID must miss")
	}
	list := r.List()
	if len(list) != 1 || list[0].ID() != "ema-cross" {
		t.Fatalf("List = %v", list)
	}
}
