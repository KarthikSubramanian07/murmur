package update

import "strings"

// newer accepts release versions only. Numeric strings avoid overflow and order
// prerelease identifiers numerically (rc.10 follows rc.2).
func newer(a, b string) bool {
	ac, ap, aok := version(a)
	bc, bp, bok := version(b)
	if !aok || !bok {
		return false
	}
	for i := range ac {
		if c := numberCompare(ac[i], bc[i]); c != 0 {
			return c > 0
		}
	}
	if ap == bp {
		return false
	}
	if ap == "" {
		return true
	}
	if bp == "" {
		return false
	}
	as, bs := strings.Split(ap, "."), strings.Split(bp, ".")
	for i := 0; i < len(as) && i < len(bs); i++ {
		x, y := as[i], bs[i]
		if x == y {
			continue
		}
		xn, yn := digits(x), digits(y)
		if xn && yn {
			return numberCompare(x, y) > 0
		}
		if xn != yn {
			return !xn
		}
		return x > y
	}
	return len(as) > len(bs)
}
func digits(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
func numberCompare(a, b string) int {
	if len(a) < len(b) {
		return -1
	}
	if len(a) > len(b) {
		return 1
	}
	return strings.Compare(a, b)
}
func version(s string) ([]string, string, bool) {
	s = strings.TrimPrefix(s, "v")
	s, _, _ = strings.Cut(s, "+")
	core, pre, hasPre := strings.Cut(s, "-")
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return nil, "", false
	}
	for _, p := range parts {
		if !digits(p) || (len(p) > 1 && p[0] == '0') {
			return nil, "", false
		}
	}
	if hasPre {
		for _, p := range strings.Split(pre, ".") {
			if p == "" || (digits(p) && len(p) > 1 && p[0] == '0') {
				return nil, "", false
			}
			for _, c := range p {
				if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '-') {
					return nil, "", false
				}
			}
		}
	}
	return parts, pre, true
}
