package project_test

import (
	"testing"

	"github.com/toidicode/taskflow/internal/project"
)

func TestNew(t *testing.T) {
	p := project.New(" Website bán hàng ", 7)
	if p.ID != 0 || p.Name != "Website bán hàng" || p.OwnerID != 7 {
		t.Errorf("New() = {#%d %q, owner %d}, muốn {#0 %q, owner 7}", p.ID, p.Name, p.OwnerID, "Website bán hàng")
	}
}
