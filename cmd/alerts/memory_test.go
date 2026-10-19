package main

import (
	"reflect"
	"testing"
)

func TestMemoryIsTunedUnlessThePersonSetItThemselves(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		env       map[string]string
		wantProcs []int
		wantGC    []int
	}{
		{"nothing set", nil, []int{1}, []int{20}},
		{"processors set", map[string]string{"GOMAXPROCS": "4"}, nil, []int{20}},
		{"garbage collection set", map[string]string{"GOGC": "100"}, []int{1}, nil},
		{"both set", map[string]string{"GOMAXPROCS": "4", "GOGC": "100"}, nil, nil},
		{"blank counts as not set", map[string]string{"GOMAXPROCS": "", "GOGC": ""}, []int{1}, []int{20}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var procs, gc []int

			tuneMemory(
				func(key string) string { return tt.env[key] },
				func(n int) int { procs = append(procs, n); return 8 },
				func(n int) int { gc = append(gc, n); return 100 },
			)

			if !reflect.DeepEqual(procs, tt.wantProcs) {
				t.Errorf("processors set to %v, want %v: tuned unless the person chose a value", procs, tt.wantProcs)
			}
			if !reflect.DeepEqual(gc, tt.wantGC) {
				t.Errorf("garbage collection set to %v, want %v: tuned unless the person chose a value", gc, tt.wantGC)
			}
		})
	}
}
