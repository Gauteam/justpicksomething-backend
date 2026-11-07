package server

import (
	"strings"
	"testing"

	"github.com/Gauteam/justpicksomething-backend/pkg/db"
)

func TestCleanNewColumns(t *testing.T) {
	tests := []struct {
		name    string
		in      []db.NewColumn
		want    int
		wantErr bool
	}{
		{"none", nil, 0, false},
		{"template", []db.NewColumn{{Name: "Link", Type: "text"}, {Name: " Hot tub ", Type: "boolean"}, {Name: "Notes", Type: "textarea"}}, 3, false},
		{"bad type", []db.NewColumn{{Name: "Beds", Type: "number"}}, 0, true},
		{"blank name", []db.NewColumn{{Name: "  ", Type: "text"}}, 0, true},
		{"duplicate ignoring case", []db.NewColumn{{Name: "Pool", Type: "boolean"}, {Name: "pool", Type: "boolean"}}, 0, true},
		{"too long name", []db.NewColumn{{Name: strings.Repeat("a", maxColumnNameLen+1), Type: "text"}}, 0, true},
		{"too many", func() []db.NewColumn {
			var cols []db.NewColumn
			for i := range maxColumns + 1 {
				cols = append(cols, db.NewColumn{Name: strings.Repeat("c", i+1), Type: "text"})
			}
			return cols
		}(), 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := cleanNewColumns(tt.in)
			if (err != nil) != tt.wantErr {
				t.Fatalf("error = %v, wantErr %v", err, tt.wantErr)
			}
			if len(got) != tt.want {
				t.Errorf("got %d columns, want %d", len(got), tt.want)
			}
		})
	}

	got, _ := cleanNewColumns([]db.NewColumn{{Name: " Hot tub ", Type: "boolean"}})
	if got[0].Name != "Hot tub" {
		t.Errorf("name not trimmed: %q", got[0].Name)
	}
}
