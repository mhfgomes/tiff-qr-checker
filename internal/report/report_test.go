package report

import "testing"

func TestExitCode(t *testing.T) {
	cases := []struct {
		name   string
		sum    Summary
		strict bool
		err    error
		want   int
	}{
		{name: "run error", err: assertErr{}, want: 2},
		{name: "strict file error", sum: Summary{FilesError: 1}, strict: true, want: 2},
		{name: "found", sum: Summary{FilesFound: 1}, want: 0},
		{name: "miss", sum: Summary{}, want: 1},
	}

	for _, tc := range cases {
		if got := ExitCode(tc.sum, tc.strict, tc.err); got != tc.want {
			t.Fatalf("%s: got %d want %d", tc.name, got, tc.want)
		}
	}
}

type assertErr struct{}

func (assertErr) Error() string { return "boom" }

