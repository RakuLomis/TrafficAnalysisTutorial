package protocol

type Limits struct {
	MaxBodyBytes       int
	MaxCiphertextBytes int
	MaxWSMessageBytes  int
	MaxNicknameRunes   int
}

func DefaultLimits() Limits {
	return Limits{
		MaxBodyBytes:       4096,
		MaxCiphertextBytes: 32768,
		MaxWSMessageBytes:  65536,
		MaxNicknameRunes:   16,
	}
}

func (l Limits) withDefaults() Limits {
	defaults := DefaultLimits()
	if l.MaxBodyBytes <= 0 {
		l.MaxBodyBytes = defaults.MaxBodyBytes
	}
	if l.MaxCiphertextBytes <= 0 {
		l.MaxCiphertextBytes = defaults.MaxCiphertextBytes
	}
	if l.MaxWSMessageBytes <= 0 {
		l.MaxWSMessageBytes = defaults.MaxWSMessageBytes
	}
	if l.MaxNicknameRunes <= 0 {
		l.MaxNicknameRunes = defaults.MaxNicknameRunes
	}
	return l
}
