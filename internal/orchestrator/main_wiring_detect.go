package orchestrator

import (
	"path/filepath"
	"strings"
)

type handlerWiringMode int

const (
	handlerWiringUnknown handlerWiringMode = iota
	handlerWiringFactoryFuncs
	handlerWiringRegisterHandlers
	handlerWiringInternalHandles
)

type storeWiringMode int

const (
	storeWiringUnknown storeWiringMode = iota
	storeWiringInstance
	storeWiringPackageFuncs
)

func detectHandlerWiringMode(src string) handlerWiringMode {
	sym := parseGoExportedSymbolsFromSource(src)
	has := func(name string) bool {
		for _, f := range sym.Funcs {
			if f == name {
				return true
			}
		}
		return false
	}
	if has("List") && has("Create") && has("Delete") {
		return handlerWiringFactoryFuncs
	}
	if has("RegisterHandlers") || strings.Contains(src, "func registerHandlers") {
		return handlerWiringRegisterHandlers
	}
	if strings.Contains(src, "func handle") {
		return handlerWiringInternalHandles
	}
	return handlerWiringUnknown
}

func detectStoreWiringMode(src string) storeWiringMode {
	sym := parseGoExportedSymbolsFromSource(src)
	hasType := func(name string) bool {
		for _, t := range sym.Types {
			if t == name {
				return true
			}
		}
		return false
	}
	hasFunc := func(name string) bool {
		for _, f := range sym.Funcs {
			if f == name {
				return true
			}
		}
		return false
	}
	if hasType("Store") || hasFunc("NewStore") {
		return storeWiringInstance
	}
	if hasFunc("List") && hasFunc("Create") && hasFunc("Delete") {
		return storeWiringPackageFuncs
	}
	return storeWiringUnknown
}

func parseGoExportedSymbolsFromSource(src string) goExportedSymbols {
	if strings.TrimSpace(src) == "" {
		return goExportedSymbols{}
	}
	return exportedSymbolsInContent(src)
}

func registerAPISignatureFromMainTest(snippet string) string {
	snippet = strings.TrimSpace(snippet)
	if snippet == "" {
		return ""
	}
	for _, line := range strings.Split(snippet, "\n") {
		line = strings.TrimSpace(line)
		if strings.Contains(line, "registerAPI(") && strings.HasPrefix(line, "func ") {
			return line
		}
	}
	return ""
}

func storeRelPathForMain(v WorkflowValidation) string {
	if p := firstRequiredPathSuffix(v, "/internal/store/store.go"); p != "" {
		return p
	}
	layout := strings.Trim(filepath.ToSlash(strings.TrimSpace(v.LayoutRoot)), "/")
	if layout != "" {
		return layout + "/internal/store/store.go"
	}
	return ""
}
