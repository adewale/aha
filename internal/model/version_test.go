package model_test

import (
	"testing"

	"github.com/adewale/aha/internal/model"
)

func TestProductVersionIsV020(t *testing.T) {
	if model.Version != "0.2.0" {
		t.Fatalf("model.Version=%q want 0.2.0", model.Version)
	}
}
