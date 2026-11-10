package pagecache

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"hash/crc32"
)

// v3 is disjoint from beta.5's `page` namespace and NUL envelope. Layout:
// magic/version | HTML status:u16, typeLen:u16, controlLen:u16, languageLen:u16,
// bodyLen:u32, type, control, language, body |
// navigation (same fields) | CRC32(all preceding bytes).
// Encoding/binary supplies bounded lengths; one Store.Set publishes the pair.
// Decode returns slices into the store's private copy, avoiding base64 and a
// second copy of both bodies on every hit.
var pairMagic = []byte("META-SSR\x03")

func validPage(page *GeneratedPage) bool {
	return page != nil && page.HTML.StatusCode == 200 && page.Navigation.StatusCode == 200 &&
		page.HTML.ContentType == "text/html; charset=utf-8" && page.Navigation.ContentType == "application/json" &&
		!forbiddenControl(page.HTML.CacheControl) && len(page.HTML.Body) > 0 && json.Valid(page.Navigation.Body)
}

func encodePage(page *GeneratedPage) ([]byte, error) {
	if !validPage(page) {
		return nil, errors.New("pagecache: incomplete pair")
	}
	value := make([]byte, 0, len(pairMagic)+28+len(page.HTML.ContentType)+len(page.Navigation.ContentType)+len(page.HTML.CacheControl)+len(page.Navigation.CacheControl)+len(page.HTML.ContentLanguage)+len(page.Navigation.ContentLanguage)+len(page.HTML.Body)+len(page.Navigation.Body))
	value = append(value, pairMagic...)
	for _, response := range []CachedResponse{page.HTML, page.Navigation} {
		if uint64(len(response.Body)) > uint64(^uint32(0)) || len(response.CacheControl) > 65535 || len(response.ContentLanguage) > 65535 {
			return nil, errors.New("pagecache: representation too large")
		}
		value = binary.LittleEndian.AppendUint16(value, uint16(response.StatusCode))
		value = binary.LittleEndian.AppendUint16(value, uint16(len(response.ContentType)))
		value = binary.LittleEndian.AppendUint16(value, uint16(len(response.CacheControl)))
		value = binary.LittleEndian.AppendUint16(value, uint16(len(response.ContentLanguage)))
		value = binary.LittleEndian.AppendUint32(value, uint32(len(response.Body)))
		value = append(value, response.ContentType...)
		value = append(value, response.CacheControl...)
		value = append(value, response.ContentLanguage...)
		value = append(value, response.Body...)
	}
	return binary.LittleEndian.AppendUint32(value, crc32.ChecksumIEEE(value)), nil
}

func decodePage(value []byte) (*GeneratedPage, error) {
	if len(value) < len(pairMagic)+4 || !bytes.Equal(value[:len(pairMagic)], pairMagic) {
		return nil, errors.New("pagecache: unsupported envelope")
	}
	payload := value[len(pairMagic) : len(value)-4]
	if crc32.ChecksumIEEE(value[:len(value)-4]) != binary.LittleEndian.Uint32(value[len(value)-4:]) {
		return nil, errors.New("pagecache: corrupt envelope")
	}
	var page GeneratedPage
	for _, response := range []*CachedResponse{&page.HTML, &page.Navigation} {
		if len(payload) < 12 {
			return nil, errors.New("pagecache: truncated representation")
		}
		response.StatusCode = int(binary.LittleEndian.Uint16(payload))
		typeLen := uint64(binary.LittleEndian.Uint16(payload[2:]))
		controlLen := uint64(binary.LittleEndian.Uint16(payload[4:]))
		languageLen := uint64(binary.LittleEndian.Uint16(payload[6:]))
		bodyLen := uint64(binary.LittleEndian.Uint32(payload[8:]))
		payload = payload[12:]
		if typeLen+controlLen+languageLen+bodyLen > uint64(len(payload)) {
			return nil, errors.New("pagecache: invalid representation length")
		}
		response.ContentType = string(payload[:typeLen])
		payload = payload[typeLen:]
		response.CacheControl = string(payload[:controlLen])
		payload = payload[controlLen:]
		response.ContentLanguage = string(payload[:languageLen])
		payload = payload[languageLen:]
		response.Body = payload[:bodyLen]
		payload = payload[bodyLen:]
	}
	if len(payload) != 0 || !validPage(&page) {
		return nil, errors.New("pagecache: incomplete pair")
	}
	return &page, nil
}
