// Package ssrresponse holds immutable output from one logical SSR generation.
// It carries no request objects or request-specific headers.
package ssrresponse

type Response struct {
	StatusCode      int
	ContentType     string
	CacheControl    string
	ContentLanguage string
	Body            []byte
}

type Page struct {
	HTML       Response
	Navigation Response
}
