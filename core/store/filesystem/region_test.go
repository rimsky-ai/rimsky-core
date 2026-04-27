package filesystem

import "testing"

func TestRegionsConflict(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		a, b []string
		want bool
	}{
		// --- disjoint cases ---
		{
			name: "literal disjoint",
			a:    []string{"workspace/foo"},
			b:    []string{"workspace/bar"},
			want: false,
		},
		{
			name: "literal disjoint different roots",
			a:    []string{"a/x"},
			b:    []string{"b/x"},
			want: false,
		},
		{
			name: "glob disjoint subtrees",
			a:    []string{"a/*"},
			b:    []string{"b/*"},
			want: false,
		},
		{
			name: "deep-glob disjoint subtrees",
			a:    []string{"a/**"},
			b:    []string{"b/**"},
			want: false,
		},
		{
			name: "literal vs glob in disjoint subtree",
			a:    []string{"a/foo"},
			b:    []string{"b/*"},
			want: false,
		},

		// --- overlapping cases ---
		{
			name: "literal equal",
			a:    []string{"a/foo"},
			b:    []string{"a/foo"},
			want: true,
		},
		{
			name: "literal contained in glob",
			a:    []string{"a/foo"},
			b:    []string{"a/*"},
			want: true,
		},
		{
			name: "glob contains literal via deep-glob",
			a:    []string{"a/sub/file"},
			b:    []string{"a/**"},
			want: true,
		},
		{
			name: "two globs with shared prefix",
			a:    []string{"a/*.txt"},
			b:    []string{"a/foo.*"},
			want: true,
		},
		{
			name: "deep-glob overlaps single-level glob",
			a:    []string{"a/**"},
			b:    []string{"a/*"},
			want: true,
		},
		{
			name: "deep-glob overlaps deep literal in same root",
			a:    []string{"a/**"},
			b:    []string{"a/sub/deep/file"},
			want: true,
		},
		{
			name: "either side empty stays disjoint",
			a:    []string{},
			b:    []string{"anywhere/*"},
			want: false,
		},

		// --- multi-glob lists; one pair conflicts ---
		{
			name: "multi-glob with one overlapping pair",
			a:    []string{"a/*", "b/*"},
			b:    []string{"c/*", "b/foo"},
			want: true,
		},
		{
			name: "multi-glob with all disjoint pairs",
			a:    []string{"a/*", "b/*"},
			b:    []string{"c/*", "d/*"},
			want: false,
		},

		// --- ** semantics ---
		{
			name: "double-star matches under prefix",
			a:    []string{"workspace/**"},
			b:    []string{"workspace/x/y/z.txt"},
			want: true,
		},
		{
			name: "double-star against sibling",
			a:    []string{"workspace/foo/**"},
			b:    []string{"workspace/bar/x"},
			want: false,
		},
		{
			name: "double-star intersects single-component glob",
			a:    []string{"workspace/foo/**"},
			b:    []string{"workspace/foo/*"},
			want: true,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := RegionsConflict(tc.a, tc.b)
			if got != tc.want {
				t.Fatalf("RegionsConflict(%v, %v) = %v, want %v", tc.a, tc.b, got, tc.want)
			}
			// Symmetry: regions conflict iff their swap conflicts.
			if back := RegionsConflict(tc.b, tc.a); back != tc.want {
				t.Fatalf("RegionsConflict(%v, %v) = %v, want %v (asymmetric)", tc.b, tc.a, back, tc.want)
			}
		})
	}
}

func TestGlobsOverlap(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		a, b string
		want bool
	}{
		{name: "equal literals", a: "foo/bar", b: "foo/bar", want: true},
		{name: "different literals", a: "foo/bar", b: "foo/baz", want: false},
		{name: "literal in glob", a: "foo/bar", b: "foo/*", want: true},
		{name: "literal outside glob", a: "foo/bar/baz", b: "foo/*", want: false},
		{name: "double-star matches deep", a: "foo/**", b: "foo/x/y", want: true},
		{name: "double-star against sibling", a: "foo/**", b: "bar/x", want: false},
		{name: "two globs same prefix", a: "foo/a*", b: "foo/*b", want: true},
		{name: "globs disjoint prefixes", a: "foo/*", b: "bar/*", want: false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := globsOverlap(tc.a, tc.b); got != tc.want {
				t.Fatalf("globsOverlap(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
			}
			if got := globsOverlap(tc.b, tc.a); got != tc.want {
				t.Fatalf("globsOverlap(%q, %q) = %v, want %v (asymmetric)", tc.b, tc.a, got, tc.want)
			}
		})
	}
}
