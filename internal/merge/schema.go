package merge

// Kind picks how a field is diffed and how its hunks are shaped.
type Kind string

const (
	Prose   Kind = "prose"
	List    Kind = "list"
	JSON    Kind = "json"
	Scalar  Kind = "scalar"
	Entries Kind = "entries"
)

// Schema maps a top-level field to its kind; unlisted fields are scalar.
type Schema map[string]Kind

var (
	Card = Schema{
		"description":               Prose,
		"personality":               Prose,
		"scenario":                  Prose,
		"first_mes":                 Prose,
		"mes_example":               Prose,
		"system_prompt":             Prose,
		"post_history_instructions": Prose,
		"creator_notes":             Prose,
		"tags":                      List,
		"alternate_greetings":       List,
		"extensions":                JSON,
	}
	Lorebook = Schema{"entries": Entries, "extensions": JSON}
	entry    = Schema{"content": Prose, "extensions": JSON}
)
