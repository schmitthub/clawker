package mocks

type FakeTerm struct{}

func (t FakeTerm) IsTTY() bool {
	return false
}

func (t FakeTerm) IsColorEnabled() bool {
	return false
}

func (t FakeTerm) Is256ColorSupported() bool {
	return false
}

func (t FakeTerm) IsTrueColorSupported() bool {
	return false
}

func (t FakeTerm) Theme() string {
	return ""
}

func (t FakeTerm) Size() (int, int, error) {
	return 80, -1, nil
}
