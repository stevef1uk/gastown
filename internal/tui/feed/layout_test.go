package feed

import (
	"strings"
	"testing"
)

func TestHiddenConvoyLayoutGivesTreeExtraHeight(t *testing.T) {
	m := NewModel(nil)
	m.SetShowConvoys(false)
	m.mu.Lock()
	m.width = 100
	m.height = 40
	m.mu.Unlock()
	m.updateViewportSizes()

	available := 40 - 1 - 1 - 1 - 4
	wantTree := available * (treePanelPercent + convoyPanelPercent) / 100

	m.mu.RLock()
	treeHeight := m.treeViewport.Height
	convoyHeight := m.convoyViewport.Height
	feedHeight := m.feedViewport.Height
	m.mu.RUnlock()

	if treeHeight != wantTree {
		t.Errorf("tree height = %d, want %d", treeHeight, wantTree)
	}
	if convoyHeight != 0 {
		t.Errorf("convoy height = %d, want 0", convoyHeight)
	}
	if feedHeight != available-wantTree {
		t.Errorf("feed height = %d, want %d", feedHeight, available-wantTree)
	}
}

func TestVisibleConvoyLayoutKeepsThreePanels(t *testing.T) {
	m := NewModel(nil)
	m.SetShowConvoys(true)
	m.mu.Lock()
	m.width = 100
	m.height = 40
	m.mu.Unlock()
	m.updateViewportSizes()

	available := 40 - 1 - 1 - 1 - 6

	m.mu.RLock()
	treeHeight := m.treeViewport.Height
	convoyHeight := m.convoyViewport.Height
	feedHeight := m.feedViewport.Height
	m.mu.RUnlock()

	if treeHeight != available*treePanelPercent/100 {
		t.Errorf("tree height = %d, want %d", treeHeight, available*treePanelPercent/100)
	}
	if convoyHeight != available*convoyPanelPercent/100 {
		t.Errorf("convoy height = %d, want %d", convoyHeight, available*convoyPanelPercent/100)
	}
	if feedHeight != available-treeHeight-convoyHeight {
		t.Errorf("feed height = %d, want %d", feedHeight, available-treeHeight-convoyHeight)
	}
}

func TestHiddenConvoyTabCyclesTreeAndFeed(t *testing.T) {
	m := NewModel(nil)
	m.SetShowConvoys(false)

	m.handleTabKey()
	m.mu.RLock()
	first := m.focusedPanel
	m.mu.RUnlock()
	if first != PanelFeed {
		t.Fatalf("focus after first Tab = %v, want PanelFeed", first)
	}

	m.handleTabKey()
	m.mu.RLock()
	second := m.focusedPanel
	m.mu.RUnlock()
	if second != PanelTree {
		t.Fatalf("focus after second Tab = %v, want PanelTree", second)
	}
}

func TestHiddenConvoyRenderOmitsConvoyPanel(t *testing.T) {
	m := NewModel(nil)
	m.SetShowConvoys(false)
	m.mu.Lock()
	m.width = 100
	m.height = 40
	m.mu.Unlock()
	m.updateViewportSizes()

	if output := m.View(); strings.Contains(output, "Convoys") {
		t.Fatal("hidden convoy panel still appears in feed output")
	}
}

func TestVisibleConvoyRenderIncludesConvoyPanel(t *testing.T) {
	m := NewModel(nil)
	m.SetShowConvoys(true)
	m.mu.Lock()
	m.width = 100
	m.height = 40
	m.mu.Unlock()
	m.updateViewportSizes()

	if output := m.View(); !strings.Contains(output, "Convoys") {
		t.Fatal("visible convoy panel missing from feed output")
	}
}
