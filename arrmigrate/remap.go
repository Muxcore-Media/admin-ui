package arrmigrate

import "strings"

// RemapRoot rewrites an Arr library path onto a MuxCore root folder.
//
// When fromPrefix is empty, the entire path is replaced with toRoot (afternoon
// import: every Arr title lands under one MuxCore library root).
// When fromPrefix is set, only paths under that prefix are rewritten; others
// are left unchanged. An empty toRoot is a no-op.
func RemapRoot(path, fromPrefix, toRoot string) string {
	path = canonicalPath(path)
	fromPrefix = canonicalPath(fromPrefix)
	toRoot = canonicalPath(toRoot)
	if toRoot == "" {
		return path
	}
	if fromPrefix == "" {
		return toRoot
	}
	if path == fromPrefix {
		return toRoot
	}
	if strings.HasPrefix(path, fromPrefix+"/") {
		return toRoot + path[len(fromPrefix):]
	}
	return path
}

// RemapItems copies items and rewrites RootFolderPath with RemapRoot.
func RemapItems(items []Item, fromPrefix, toRoot string) []Item {
	if canonicalPath(fromPrefix) == "" && canonicalPath(toRoot) == "" {
		return items
	}
	out := make([]Item, len(items))
	copy(out, items)
	for i := range out {
		out[i].RootFolderPath = RemapRoot(out[i].RootFolderPath, fromPrefix, toRoot)
	}
	return out
}

func canonicalPath(p string) string {
	p = strings.TrimSpace(p)
	p = strings.ReplaceAll(p, "\\", "/")
	return strings.TrimRight(p, "/")
}
