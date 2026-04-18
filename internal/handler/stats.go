package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/blackforge/embookshelf/internal/repo"
)

// statsDTO mirrors service.Stats on the wire. camelCase JSON tags match
// the TS client; the buckets reuse the repo's tiny struct shape since
// there's nothing worth translating.
type statsDTO struct {
	Totals        statsTotalsDTO          `json:"totals"`
	User          statsUserDTO            `json:"user"`
	Libraries     []bucketDTO             `json:"libraries"`
	Formats       []bucketDTO             `json:"formats"`
	TopAuthors    []bucketDTO             `json:"topAuthors"`
	TopTags       []bucketDTO             `json:"topTags"`
	YearHistogram []statsYearBucketDTO    `json:"yearHistogram"`
	Ratings       []statsRatingBucketDTO  `json:"ratings"`
}

type statsTotalsDTO struct {
	Books          int `json:"books"`
	BooksWithCover int `json:"booksWithCover"`
}

type statsUserDTO struct {
	Reading      int `json:"reading"`
	Finished     int `json:"finished"`
	Annotations  int `json:"annotations"`
	Shelves      int `json:"shelves"`
	SmartShelves int `json:"smartShelves"`
}

type bucketDTO struct {
	Label string `json:"label"`
	Count int    `json:"count"`
}

type statsYearBucketDTO struct {
	Decade int `json:"decade"`
	Count  int `json:"count"`
}

type statsRatingBucketDTO struct {
	Rating int `json:"rating"`
	Count  int `json:"count"`
}

func toBuckets(in []repo.StatsBucket) []bucketDTO {
	out := make([]bucketDTO, 0, len(in))
	for _, b := range in {
		out = append(out, bucketDTO{Label: b.Label, Count: b.Count})
	}
	return out
}

// Stats returns the full aggregate payload. One request → one page; the
// react-query cache keeps the result around so reopening the route
// feels instant.
func (h *Handler) Stats(c *gin.Context) {
	userID := requireUserID(c)
	if userID == "" {
		return
	}
	s, err := h.stats.Collect(c.Request.Context(), userID)
	if err != nil {
		writeServerError(c, "stats collect", err)
		return
	}

	dto := statsDTO{
		Totals: statsTotalsDTO{
			Books:          s.Totals.Books,
			BooksWithCover: s.Totals.BooksWithCover,
		},
		User: statsUserDTO{
			Reading:      s.User.Reading,
			Finished:     s.User.Finished,
			Annotations:  s.User.Annotations,
			Shelves:      s.User.Shelves,
			SmartShelves: s.User.SmartShelves,
		},
		Libraries:  toBuckets(s.Libraries),
		Formats:    toBuckets(s.Formats),
		TopAuthors: toBuckets(s.TopAuthors),
		TopTags:    toBuckets(s.TopTags),
	}

	dto.YearHistogram = make([]statsYearBucketDTO, 0, len(s.YearHistogram))
	for _, b := range s.YearHistogram {
		dto.YearHistogram = append(dto.YearHistogram, statsYearBucketDTO{Decade: b.Decade, Count: b.Count})
	}
	dto.Ratings = make([]statsRatingBucketDTO, 0, len(s.Ratings))
	for _, b := range s.Ratings {
		dto.Ratings = append(dto.Ratings, statsRatingBucketDTO{Rating: b.Rating, Count: b.Count})
	}

	c.JSON(http.StatusOK, gin.H{"stats": dto})
}
