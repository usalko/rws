package main

func Last[E any](s []E) E {
	if len(s) == 0 {
		var zero E
		return zero
	}
	return s[len(s)-1]
}
