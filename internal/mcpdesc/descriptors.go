package mcpdesc

func Tool(name, description string, inputSchema map[string]any) map[string]any {
	if inputSchema == nil {
		inputSchema = map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		}
	}
	return map[string]any{
		"name":        name,
		"description": description,
		"inputSchema": inputSchema,
	}
}
