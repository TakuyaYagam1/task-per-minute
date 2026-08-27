package arena

import "github.com/TakuyaYagam1/task-per-minute/internal/domain"

type SwissRoundPlan struct {
	Number      int
	SeriesSlots int
	ByeSlots    int
}

type SwissPlan struct {
	Preset     domain.ArenaPreset
	RosterSize int
	Rounds     []SwissRoundPlan
}

func BuildSwissPlan(preset domain.ArenaPreset, rosterSize int) (SwissPlan, error) {
	roundCount, err := preset.SwissRounds(rosterSize)
	if err != nil {
		return SwissPlan{}, err
	}

	rounds := make([]SwissRoundPlan, roundCount)
	for i := range rounds {
		rounds[i] = SwissRoundPlan{
			Number:      i + 1,
			SeriesSlots: rosterSize / 2,
			ByeSlots:    rosterSize % 2,
		}
	}

	return SwissPlan{
		Preset:     preset,
		RosterSize: rosterSize,
		Rounds:     rounds,
	}, nil
}
