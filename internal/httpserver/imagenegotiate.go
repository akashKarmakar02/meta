package httpserver

import (
	"strconv"
	"strings"

	"go-meta/imageopt"
	"go-meta/imageproc"
)

// Accept-header format negotiation for f=auto (F03 §17–20).
//
// A small, focused parser — not a general negotiation framework. It
// understands the subset of RFC 9110 that real browsers send:
// comma-separated media ranges with optional q weights, wildcard types
// and subtypes, and explicit q=0 exclusions.
//
// Precedence rules (documented contract):
//
//  1. Only formats the configured processor can actually encode are
//     selectable; everything else is invisible to negotiation.
//  2. Higher q wins.
//  3. On equal q, the earlier Accept element wins (browsers list their
//     preferred format first).
//  4. Within one wildcard element (image/*, */*), the server's
//     deterministic order applies: webp, avif, jpeg, png.
//  5. A missing or empty Accept header states no preference: the
//     server default (webp, else the first supported format) applies.
//  6. An Accept header that names formats but accepts none of the
//     encodable ones yields no selection (HTTP layer answers 406).
//  7. Malformed elements are ignored like browsers ignore them; a wholly
//     unparseable header is treated as no preference stated (rule 5) —
//     the response still carries a truthful, processor-supported format.

// negotiateOrder is the deterministic within-wildcard preference: smaller
// and faster encode first.
var negotiateOrder = []imageopt.Format{imageopt.FormatWebP, imageopt.FormatAVIF, imageopt.FormatJPEG, imageopt.FormatPNG}

// selectFormat resolves f=auto to a concrete, processor-supported format
// given the request's Accept header. It returns "" when the client names
// only unacceptable formats.
func selectFormat(accept string, caps imageproc.Capabilities) imageopt.Format {
	supported := map[imageopt.Format]bool{}
	for _, f := range negotiateOrder {
		if caps.Encode(f) {
			supported[f] = true
		}
	}
	if len(supported) == 0 {
		return ""
	}
	defaultFormat := firstSupported(supported)
	if strings.TrimSpace(accept) == "" {
		return defaultFormat
	}

	var best imageopt.Format
	bestQ := -1.0
	namedAny := false
	for _, element := range splitMediaTypes(accept) {
		mediaType, q, ok := parseMediaType(element)
		if !ok || q <= 0 {
			continue // malformed or explicitly excluded element
		}
		for _, f := range negotiateOrder {
			if !supported[f] {
				continue
			}
			m, _ := f.MIME()
			if !matchesRange(mediaType, m) {
				continue
			}
			namedAny = true
			if q > bestQ { // strictly greater: earlier elements win ties
				best, bestQ = f, q
			}
			break // one format per element; the next element competes on q
		}
	}
	if !namedAny && anyConcreteMention(accept) {
		return "" // client named image formats, none acceptable
	}
	if string(best) == "" {
		return defaultFormat
	}
	return best
}

// matchesRange reports whether an Accept media range covers the mime type.
func matchesRange(rangeType, mimeType string) bool {
	if rangeType == "*/*" {
		return true
	}
	rType, rSub, ok := strings.Cut(rangeType, "/")
	if !ok {
		return false
	}
	mType, mSub, _ := strings.Cut(mimeType, "/")
	if rType != "*" && rType != mType {
		return false
	}
	return rSub == "*" || rSub == mSub
}

// anyConcreteMention reports whether the header mentions any image/*
// subtype at a positive weight — used to distinguish "stated preferences,
// none acceptable" (→ 406) from "only junk/wildcards" (→ default).
func anyConcreteMention(accept string) bool {
	for _, element := range splitMediaTypes(accept) {
		mediaType, q, ok := parseMediaType(element)
		if !ok || q <= 0 || !strings.HasPrefix(mediaType, "image/") || strings.HasSuffix(mediaType, "/*") {
			continue
		}
		// A concrete image subtype (webp, avif, gif, ...): the client talks
		// images but none of ours are acceptable.
		return true
	}
	return false
}

func firstSupported(supported map[imageopt.Format]bool) imageopt.Format {
	for _, f := range negotiateOrder {
		if supported[f] {
			return f
		}
	}
	return ""
}

// splitMediaTypes splits on commas outside quoted strings.
func splitMediaTypes(accept string) []string {
	out := make([]string, 0, 4)
	var cur strings.Builder
	inQuote := false
	for _, ch := range accept {
		switch {
		case ch == '"':
			inQuote = !inQuote
			cur.WriteRune(ch)
		case ch == ',' && !inQuote:
			out = append(out, cur.String())
			cur.Reset()
		default:
			cur.WriteRune(ch)
		}
	}
	out = append(out, cur.String())
	return out
}

// parseMediaType parses one Accept element: type/subtype plus optional
// parameters, honoring q= (and ignoring other params). ok is false for
// structurally invalid elements (no slash, embedded whitespace, bad q).
func parseMediaType(element string) (mediaType string, q float64, ok bool) {
	parts := strings.SplitN(element, ";", 2)
	mediaType = strings.ToLower(strings.TrimSpace(parts[0]))
	if !strings.Contains(mediaType, "/") || strings.ContainsAny(mediaType, " \t") {
		return "", 0, false
	}
	q = 1.0 // default weight per RFC 9110
	if len(parts) == 2 {
		for _, param := range strings.Split(parts[1], ";") {
			k, v, found := strings.Cut(param, "=")
			if !found {
				continue
			}
			if strings.EqualFold(strings.TrimSpace(k), "q") {
				f, err := strconv.ParseFloat(strings.Trim(strings.TrimSpace(v), `"`), 64)
				if err != nil || f < 0 || f > 1 {
					return "", 0, false // malformed q invalidates the element
				}
				q = f
			}
		}
	}
	return mediaType, q, true
}
