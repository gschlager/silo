package agents

import (
	"encoding/json"
	"os"
)

// mergeJSONKeys reads source JSON, extracts only the listed keys, and merges
// them into the destination file. Keys not in the list are preserved in the
// destination. If the destination doesn't exist, it's created with only the
// listed keys. If the source doesn't exist, this is a no-op.
func mergeJSONKeys(src, dst string, keys []string) error {
	srcData, err := os.ReadFile(src)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	var srcMap map[string]json.RawMessage
	if err := json.Unmarshal(srcData, &srcMap); err != nil {
		return err
	}

	// Read destination if it exists.
	dstMap := make(map[string]json.RawMessage)
	if dstData, err := os.ReadFile(dst); err == nil {
		json.Unmarshal(dstData, &dstMap)
	}

	// Merge only the listed keys from source into destination.
	for _, key := range keys {
		if val, ok := srcMap[key]; ok {
			dstMap[key] = val
		}
	}

	out, err := json.MarshalIndent(dstMap, "", "  ")
	if err != nil {
		return err
	}

	if err := os.MkdirAll(dir(dst), 0700); err != nil {
		return err
	}
	return os.WriteFile(dst, append(out, '\n'), 0600)
}

// extractJSONKeys reads a JSON file and returns only the listed keys as bytes.
func extractJSONKeys(path string, keys []string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var full map[string]json.RawMessage
	if err := json.Unmarshal(data, &full); err != nil {
		return nil, err
	}

	extracted := make(map[string]json.RawMessage)
	for _, key := range keys {
		if val, ok := full[key]; ok {
			extracted[key] = val
		}
	}

	return json.MarshalIndent(extracted, "", "  ")
}

// deepMergeJSONFile reads a JSON file and deep-merges the given values into it.
// If the file doesn't exist, it's created. Existing keys at any depth are preserved
// unless overridden by the merge values.
func deepMergeJSONFile(path string, values map[string]any) error {
	existing := make(map[string]any)
	if data, err := os.ReadFile(path); err == nil {
		json.Unmarshal(data, &existing)
	}

	deepMerge(existing, values)

	out, err := json.MarshalIndent(existing, "", "  ")
	if err != nil {
		return err
	}

	if err := os.MkdirAll(dir(path), 0700); err != nil {
		return err
	}
	return os.WriteFile(path, append(out, '\n'), 0600)
}

// deepMerge recursively merges src into dst. Maps are merged recursively,
// other values are overwritten.
func deepMerge(dst, src map[string]any) {
	for k, srcVal := range src {
		if srcMap, ok := srcVal.(map[string]any); ok {
			if dstMap, ok := dst[k].(map[string]any); ok {
				deepMerge(dstMap, srcMap)
				continue
			}
		}
		dst[k] = srcVal
	}
}

func dir(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' {
			return path[:i]
		}
	}
	return "."
}
