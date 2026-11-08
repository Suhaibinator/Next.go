package routing

import (
	"fmt"
	"testing"
)

func benchmarkRoutes(n int) []string {
	p := make([]string, n)
	for i := range p {
		p[i] = fmt.Sprintf("/records/r%05d/:id", i)
	}
	return p
}

func BenchmarkRouteDispatch(b *testing.B) {
	for _, n := range []int{10, 100, 1000, 3000} {
		patterns := benchmarkRoutes(n)
		path := fmt.Sprintf("/records/r%05d/value", n-1)
		index, err := NewIndex(patterns)
		if err != nil {
			b.Fatal(err)
		}
		b.Run(fmt.Sprintf("linear/%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				found := false
				for _, pattern := range patterns {
					if _, ok := Match(pattern, path); ok {
						found = true
						break
					}
				}
				if !found {
					b.Fatal("not found")
				}
			}
		})
		b.Run(fmt.Sprintf("compiled/%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				at, params, ok := index.Match(path)
				if !ok || at != n-1 || params["id"] != "value" {
					b.Fatal(at, params, ok)
				}
			}
		})
	}
}

func BenchmarkRouteValidation(b *testing.B) {
	for _, n := range []int{10, 100, 1000, 3000} {
		patterns := benchmarkRoutes(n)
		b.Run(fmt.Sprintf("linear/%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if err := validateLinearReference(patterns); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run(fmt.Sprintf("indexed/%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if err := ValidatePatterns(patterns); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run(fmt.Sprintf("compile/%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if _, err := NewIndex(patterns); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
