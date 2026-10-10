package browser

import (
	"bytes"
	"encoding/binary"
	"encoding/xml"
	"errors"
	"io"
	"os"
	"unicode/utf16"
)

func readPlistStrings(path string) (map[string]string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return parsePlistStrings(b)
}

func parsePlistStrings(b []byte) (map[string]string, error) {
	if bytes.HasPrefix(b, []byte("bplist00")) {
		return parseBinaryPlist(b)
	}
	return parseXMLPlist(b)
}

func parseXMLPlist(b []byte) (map[string]string, error) {
	d := xml.NewDecoder(bytes.NewReader(b))
	d.Strict = false
	if err := seekElement(d, "dict"); err != nil {
		return nil, err
	}
	out := map[string]string{}
	key := ""
	for {
		tok, err := d.Token()
		if err != nil {
			return out, nil
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if err := xmlDictChild(d, t, &key, out); err != nil {
				return out, nil
			}
		case xml.EndElement:
			if t.Name.Local == "dict" {
				return out, nil
			}
		}
	}
}

func xmlDictChild(d *xml.Decoder, t xml.StartElement, key *string, out map[string]string) error {
	switch t.Name.Local {
	case "key":
		var s string
		if err := d.DecodeElement(&s, &t); err != nil {
			return err
		}
		*key = s
	case "string":
		var s string
		if err := d.DecodeElement(&s, &t); err != nil {
			return err
		}
		if *key != "" {
			out[*key] = s
		}
		*key = ""
	default:
		*key = ""
		return d.Skip()
	}
	return nil
}

func seekElement(d *xml.Decoder, name string) error {
	for {
		tok, err := d.Token()
		if err != nil {
			return err
		}
		if s, ok := tok.(xml.StartElement); ok && s.Name.Local == name {
			return nil
		}
	}
}

type bplist struct {
	b       []byte
	offsets []uint64
	refSize int
}

var errBadPlist = errors.New("malformed binary plist")

func parseBinaryPlist(b []byte) (map[string]string, error) {
	if len(b) < 40 {
		return nil, errBadPlist
	}
	tr := b[len(b)-32:]
	offSize, refSize := int(tr[6]), int(tr[7])
	num := binary.BigEndian.Uint64(tr[8:16])
	top := binary.BigEndian.Uint64(tr[16:24])
	tableOff := binary.BigEndian.Uint64(tr[24:32])
	if offSize < 1 || offSize > 8 || refSize < 1 || refSize > 8 || num > uint64(len(b)) || tableOff+num*uint64(offSize) > uint64(len(b)) {
		return nil, errBadPlist
	}
	p := &bplist{b: b, refSize: refSize, offsets: make([]uint64, num)}
	for i := range p.offsets {
		p.offsets[i] = readUint(b[tableOff+uint64(i*offSize):], offSize)
	}
	return p.topDict(top)
}

func readUint(b []byte, n int) uint64 {
	var v uint64
	for i := 0; i < n && i < len(b); i++ {
		v = v<<8 | uint64(b[i])
	}
	return v
}

func (p *bplist) object(ref uint64) (byte, uint64, uint64, error) {
	if ref >= uint64(len(p.offsets)) {
		return 0, 0, 0, errBadPlist
	}
	off := p.offsets[ref]
	if off >= uint64(len(p.b)) {
		return 0, 0, 0, errBadPlist
	}
	marker := p.b[off]
	kind, count := marker>>4, uint64(marker&0x0f)
	pos := off + 1
	if count == 0x0f && kind != 0x0 && kind != 0x1 && kind != 0x2 && kind != 0x3 {
		if pos >= uint64(len(p.b)) {
			return 0, 0, 0, errBadPlist
		}
		im := p.b[pos]
		if im>>4 != 0x1 {
			return 0, 0, 0, errBadPlist
		}
		n := 1 << (im & 0x0f)
		if pos+1+uint64(n) > uint64(len(p.b)) {
			return 0, 0, 0, errBadPlist
		}
		count = readUint(p.b[pos+1:], n)
		pos += 1 + uint64(n)
	}
	return kind, count, pos, nil
}

func (p *bplist) str(ref uint64) (string, bool) {
	kind, count, pos, err := p.object(ref)
	if err != nil {
		return "", false
	}
	switch kind {
	case 0x5:
		if pos+count > uint64(len(p.b)) {
			return "", false
		}
		return string(p.b[pos : pos+count]), true
	case 0x6:
		if pos+2*count > uint64(len(p.b)) {
			return "", false
		}
		u := make([]uint16, count)
		for i := range u {
			u[i] = binary.BigEndian.Uint16(p.b[pos+uint64(2*i):])
		}
		return string(utf16.Decode(u)), true
	}
	return "", false
}

func (p *bplist) topDict(ref uint64) (map[string]string, error) {
	kind, count, pos, err := p.object(ref)
	if err != nil || kind != 0xd {
		return nil, errBadPlist
	}
	rs := uint64(p.refSize)
	if pos+2*count*rs > uint64(len(p.b)) {
		return nil, io.ErrUnexpectedEOF
	}
	out := map[string]string{}
	for i := uint64(0); i < count; i++ {
		k, ok := p.str(readUint(p.b[pos+i*rs:], p.refSize))
		if !ok {
			continue
		}
		if v, ok := p.str(readUint(p.b[pos+(count+i)*rs:], p.refSize)); ok {
			out[k] = v
		}
	}
	return out, nil
}
