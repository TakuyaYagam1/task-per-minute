package leaderboard

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

var ErrInvalidPageQuery = errors.New("invalid leaderboard page query")

type PageUseCase struct {
	repository PageRepository
}

func NewPageUseCase(repository PageRepository) *PageUseCase {
	return &PageUseCase{repository: repository}
}

var _ PageReader = (*PageUseCase)(nil)

func (u *PageUseCase) Page(ctx context.Context, query PageQuery) (PageResult, error) {
	query, err := normalizePageQuery(query)
	if err != nil {
		return PageResult{}, err
	}

	rows, err := u.repository.LeaderboardPage(ctx, query)
	if err != nil {
		return PageResult{}, fmt.Errorf("leaderboard page: load rows: %w", err)
	}
	if rows.Total < 0 || len(rows.Entries) > int(query.PerPage) {
		return PageResult{}, errors.New("leaderboard page: repository returned invalid page data")
	}

	var totalPages int64
	if rows.Total > 0 {
		totalPages = (rows.Total-1)/int64(query.PerPage) + 1
	}
	return PageResult{
		Entries:    rows.Entries,
		Page:       query.Page,
		PerPage:    query.PerPage,
		Total:      rows.Total,
		TotalPages: totalPages,
	}, nil
}

func normalizePageQuery(query PageQuery) (PageQuery, error) {
	query.Search = strings.TrimSpace(query.Search)
	if utf8.RuneCountInString(query.Search) > MaxSearchLength {
		return PageQuery{}, ErrInvalidPageQuery
	}
	if query.Wins == "" {
		query.Wins = WinsAll
	}
	switch query.Wins {
	case WinsAll, WinsWithWins, WinsWithoutWins:
	default:
		return PageQuery{}, ErrInvalidPageQuery
	}
	if query.Page == 0 {
		query.Page = 1
	}
	if query.PerPage == 0 {
		query.PerPage = DefaultPageSize
	}
	if query.Page < 1 || query.PerPage < 1 || query.PerPage > MaxPageSize {
		return PageQuery{}, ErrInvalidPageQuery
	}
	return query, nil
}
