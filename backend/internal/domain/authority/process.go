package authority

type ProcessKind string

const (
	ProcessAuthority         ProcessKind = "authority"
	ProcessProjection        ProcessKind = "projection"
	ProcessReadOnlyTransport ProcessKind = "read_only_transport"
)

func (kind ProcessKind) IsValid() bool {
	return kind == ProcessAuthority || kind == ProcessProjection ||
		kind == ProcessReadOnlyTransport
}
