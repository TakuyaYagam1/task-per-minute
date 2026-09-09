package game

import "reflect"

func validateReconnectCurrentOutcome(authority ReconnectAuthority) error {
	if authority.Game.State.IsTerminal() {
		if authority.Current == nil || validateReconnectTerminalOutcome(authority, *authority.Current) != nil {
			return reconnectError("terminal Game lacks current outcome")
		}
		return nil
	}
	if authority.Current != nil {
		return reconnectError("live Game carries terminal outcome")
	}
	return nil
}

func validateReconnectRecord(record ReconnectRecord) error {
	if err := validateReconnectRecordHeader(record); err != nil {
		return err
	}
	if err := validateReconnectRecordCommand(record); err != nil {
		return err
	}
	if record.ReconnectAuthority.Current == nil {
		return validateLiveReconnectRecord(record)
	}
	if err := validateTerminalReconnectRecord(record); err != nil {
		return err
	}
	if record.ReconnectAuthority.Revision == record.ExpectedAuthorityRevision+1 && !reconnectRecordSettlementMatches(record) {
		return reconnectError("terminal outcome does not match command identities")
	}
	return nil
}

func validateReconnectRecordHeader(record ReconnectRecord) error {
	if record.ExpectedAuthorityRevision < 1 || !reconnectValidServerTime(record.RecordedAt) || validateReconnectAuthority(record.ReconnectAuthority) != nil {
		return reconnectError("invalid record header")
	}
	if record.ReconnectAuthority.Revision != record.ExpectedAuthorityRevision && record.ReconnectAuthority.Revision != record.ExpectedAuthorityRevision+1 {
		return reconnectError("invalid record command or revision")
	}
	return nil
}

func validateReconnectRecordCommand(record ReconnectRecord) error {
	if reconnectRecordCommandCount(record) != 1 {
		return reconnectError("invalid record command or revision")
	}
	switch record.Kind {
	case MutationReconnect:
		if record.ReconnectCommand == nil || !validReconnectCommand(*record.ReconnectCommand) || record.ReconnectCommand.Scope != record.ReconnectAuthority.Scope {
			return reconnectError("invalid reconnect receipt")
		}
	case MutationTimeout:
		if record.TimeoutCommand == nil || !validReconnectTimeoutCommand(*record.TimeoutCommand) || record.TimeoutCommand.Scope != record.ReconnectAuthority.Scope {
			return reconnectError("invalid timeout receipt")
		}
	case MutationDisconnect:
		if record.DisconnectCommand == nil || !validDisconnectCommand(*record.DisconnectCommand) || record.DisconnectCommand.Scope != record.ReconnectAuthority.Scope {
			return reconnectError("invalid disconnect receipt")
		}
	default:
		return reconnectError("invalid record command or revision")
	}
	return nil
}

func reconnectRecordCommandCount(record ReconnectRecord) int {
	commands := 0
	if record.ReconnectCommand != nil {
		commands++
	}
	if record.TimeoutCommand != nil {
		commands++
	}
	if record.DisconnectCommand != nil {
		commands++
	}
	return commands
}

func validateLiveReconnectRecord(record ReconnectRecord) error {
	if record.GameResultRevision != nil || record.VoidGameResultRevision != nil || record.ScoreRevision != nil ||
		record.SeriesResultRevision != nil || record.ReplayRoute != nil || record.Evidence != nil {
		return reconnectError("live mutation carries terminal evidence")
	}
	return nil
}

func validateTerminalReconnectRecord(record ReconnectRecord) error {
	current := record.ReconnectAuthority.Current
	if !reflect.DeepEqual(record.GameResultRevision, current.GameResultRevision) ||
		!reflect.DeepEqual(record.VoidGameResultRevision, current.VoidGameResultRevision) ||
		record.ScoreRevision == nil || !reflect.DeepEqual(*record.ScoreRevision, current.ScoreRevision) ||
		!reflect.DeepEqual(record.SeriesResultRevision, current.SeriesResultRevision) ||
		!reflect.DeepEqual(record.ReplayRoute, current.ReplayRoute) || record.Evidence == nil ||
		!reflect.DeepEqual(*record.Evidence, current.Evidence) || !record.RecordedAt.Equal(current.TerminalizedAt) {
		return reconnectError("record lost terminal outcome")
	}
	return nil
}
