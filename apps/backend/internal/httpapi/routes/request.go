package routes

import (
	"encoding/json"
	"errors"
	"io"
	"strconv"

	"github.com/gin-gonic/gin"
)

// decodeStrictJSON rejects unknown fields and trailing JSON values so retired
// or misspelled request fields fail instead of being silently ignored.
func decodeStrictJSON(c *gin.Context, value any) error {
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values are not allowed")
		}
		return err
	}
	return nil
}

// parsePageInts applies the shared bounded staff recovery pagination contract.
func parsePageInts(limitRaw, offsetRaw string) (int, int, error) {
	limit := 50
	offset := 0
	var err error
	if limitRaw != "" {
		limit, err = strconv.Atoi(limitRaw)
		if err != nil || limit < 1 || limit > 100 {
			return 0, 0, errors.New("invalid limit")
		}
	}
	if offsetRaw != "" {
		offset, err = strconv.Atoi(offsetRaw)
		if err != nil || offset < 0 {
			return 0, 0, errors.New("invalid offset")
		}
	}
	return limit, offset, nil
}
