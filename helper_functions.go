package omnipaxos

func (r Role) String() string {
	switch r {
	case LEADER:
		return "LEADER"
	case FOLLOWER:
		return "FOLLOWER"
	default:
		return "UNKNOWN_ROLE"
	}
}

func (p Phase) String() string {
	switch p {
	case PREPARE:
		return "PREPARE"
	case PROMISE:
		return "PROMISE"
	case ACCEPTSYNC:
		return "ACCEPTSYNC"
	case ACCEPT:
		return "ACCEPT"
	case DECIDE:
		return "DECIDE"
	case RECOVER:
		return "RECOVER"
	default:
		return "UNKNOWN_PHASE"
	}
}

// TODO make this a real implementation
func (op *OmniPaxos) stopped() bool {
	return op.killed()
}

func (op *OmniPaxos) prefix(idx int) []any {
	if idx < 0 {
		return []any{}
	}
	if idx > len(op.log) {
		return nil
	}
	return op.log[:idx]
}

func (op *OmniPaxos) suffix(idx int) []any {
	if idx < 0 {
		return op.log
	}
	if idx > len(op.log) {
		return nil
	}
	return op.log[idx:]
}
