package agent

// opusPacketSamples returns the decoded duration at the mandatory 48 kHz RTP
// clock rate, from RFC 6716 section 3. Capture PTS may include scheduling jitter.
func opusPacketSamples(packet []byte) uint32 {
	if len(packet) == 0 {
		return 0
	}
	toc := packet[0]
	var samples uint32
	switch {
	case toc&0x80 != 0:
		samples = 120 << ((toc >> 3) & 3)
	case toc&0x60 == 0x60:
		samples = 480 << ((toc >> 3) & 1)
	case (toc>>3)&3 == 3:
		samples = 2880
	default:
		samples = 480 << ((toc >> 3) & 3)
	}
	frames := uint32(1)
	switch toc & 3 {
	case 1, 2:
		frames = 2
	case 3:
		if len(packet) < 2 {
			return 0
		}
		frames = uint32(packet[1] & 0x3f)
	}
	samples *= frames
	if samples > 5760 {
		return 0
	}
	return samples
}
