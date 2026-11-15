package project_test

import (
	"testing"

	"github.com/toidicode/taskflow/internal/project"
)

func TestNew(t *testing.T) {
	p := project.New(1, " Website bán hàng ", 7)
	if p.Name != "Website bán hàng" || p.OwnerID != 7 {
		t.Errorf("New() = {%q, owner %d}, muốn {%q, owner 7}", p.Name, p.OwnerID, "Website bán hàng")
	}
}
