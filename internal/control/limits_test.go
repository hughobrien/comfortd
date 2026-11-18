// SPDX-License-Identifier: GPL-3.0-or-later

package control

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/hughobrien/comfortd/internal/store"
)

type fakeLimits struct {
	calls [][3]any
	err   error
}

func (f *fakeLimits) SetLimits(_ context.Context, serial string, lo, hi float64) error {
	f.calls = append(f.calls, [3]any{serial, lo, hi})
	return f.err
}

func TestSetLimits(t *testing.T) {
	prof, err := os.ReadFile("../../pkg/kumo/kumotest/fixtures/profile_house.json") // cool 19-30, heat 10-28
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name      string
		lo, hi    float64
		enabled   bool
		sendErr   error
		wantCalls int
		wantErr   error
	}{
		{"ok", 19, 24, true, nil, 1, nil},
		{"half steps", 19.5, 24.5, true, nil, 1, nil},
		{"not in limit units", 19, 24, false, nil, 0, ErrNotWritable},
		{"floor below profile cool min", 18, 24, true, nil, 0, ErrInvalid},
		{"ceiling above profile heat max", 19, 29, true, nil, 0, ErrInvalid},
		{"floor not below ceiling", 24, 24, true, nil, 0, ErrInvalid},
		{"not a half step", 19.3, 24, true, nil, 0, ErrInvalid},
		{"cloud refuses", 19, 24, true, errors.New("boom"), 1, errors.New("boom")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := store.New([]store.Unit{{Slug: "house", Serial: "SER"}})
			if err := st.Put("house", store.EndpointProfile, prof, time.Now()); err != nil {
				t.Fatal(err)
			}
			if err := st.Put("house", store.EndpointStatus, []byte(`{"minSetPoint":19.5,"maxSetPoint":28}`), time.Now()); err != nil {
				t.Fatal(err)
			}
			f := &fakeLimits{err: tc.sendErr}
			o := Options{Store: st, Writable: []string{"house"}, Limits: f}
			if tc.enabled {
				o.LimitUnits = []string{"house"}
			}
			err := New(o).SetLimits(context.Background(), "house", tc.lo, tc.hi, "ui")
			if (err == nil) != (tc.wantErr == nil) || (tc.wantErr != nil && tc.sendErr == nil && !errors.Is(err, tc.wantErr)) {
				t.Fatalf("err %v, want %v", err, tc.wantErr)
			}
			if len(f.calls) != tc.wantCalls {
				t.Fatalf("calls %v", f.calls)
			}
			if tc.wantCalls == 1 && f.calls[0] != [3]any{"SER", tc.lo, tc.hi} {
				t.Fatalf("sent %v", f.calls[0])
			}
		})
	}
}
