package link

func DebuggerWriteMemoryStream() *Stream {
	l := &Link{streams: map[uint32]*Stream{}}
	s := newStream(l, &channel{l: l, control: true}, 1, "cdp", nil, false)
	s.estab = true
	l.streams[s.id] = s
	return s
}
