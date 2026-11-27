package content

// API response types. Each is an allowlist: a field Metabase sends that is
// not named here is dropped when the response is decoded, so nothing new
// reaches an export by accident.

import (
	"encoding/json"
	"strings"
)

// ---------------------------------------------------------------- API allowlist

type apiCollection struct {
	ID              json.RawMessage `json:"id"` // the root collection's ID is the string "root"
	Name            string          `json:"name"`
	Location        string          `json:"location"`
	PersonalOwnerID *int            `json:"personal_owner_id"`
	Archived        bool            `json:"archived"`
}

type apiListItem struct {
	ID           int    `json:"id"`
	Name         string `json:"name"`
	Type         string `json:"type"`
	Dataset      bool   `json:"dataset"`
	Model        string `json:"model"` // only in /api/search results
	CollectionID *int   `json:"collection_id"`
	DatabaseID   *int   `json:"database_id"`
	Collection   *struct {
		ID json.RawMessage `json:"id"`
	} `json:"collection"` // /api/search nests it
	Archived bool `json:"archived"`
}

type apiCard struct {
	ID             int             `json:"id"`
	Name           string          `json:"name"`
	Description    *string         `json:"description"`
	Type           string          `json:"type"`
	Dataset        bool            `json:"dataset"` // older versions mark models this way
	Display        string          `json:"display"`
	CollectionID   *int            `json:"collection_id"`
	DatabaseID     *int            `json:"database_id"`
	UpdatedAt      string          `json:"updated_at"`
	Archived       bool            `json:"archived"`
	DatasetQuery   json.RawMessage `json:"dataset_query"`
	ResultMetadata []struct {
		Name     string `json:"name"`
		BaseType string `json:"base_type"`
	} `json:"result_metadata"`
}

type apiTag struct {
	Name        string          `json:"name"`
	DisplayName string          `json:"display-name"`
	Type        string          `json:"type"`
	CardID      *int            `json:"card-id"`
	SnippetName string          `json:"snippet-name"`
	WidgetType  string          `json:"widget-type"`
	Required    bool            `json:"required"`
	Dimension   json.RawMessage `json:"dimension"`
}

type apiNative struct {
	Query        string            `json:"query"`
	TemplateTags map[string]apiTag `json:"template-tags"`
}

type apiQuery struct {
	Type     string            `json:"type"`
	Database int               `json:"database"`
	Native   *apiNative        `json:"native"`
	Stages   []json.RawMessage `json:"stages"` // MBQL 5, Metabase 0.57 and later
}

type apiStage struct {
	LibType      string            `json:"lib/type"`
	Native       string            `json:"native"`
	TemplateTags map[string]apiTag `json:"template-tags"`
}

type apiDashboard struct {
	ID           int     `json:"id"`
	Name         string  `json:"name"`
	Description  *string `json:"description"`
	CollectionID *int    `json:"collection_id"`
	Archived     bool    `json:"archived"`
	Parameters   []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
		Slug string `json:"slug"`
		Type string `json:"type"`
	} `json:"parameters"`
	Tabs []struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	} `json:"tabs"`
	Dashcards    []apiDashcard `json:"dashcards"`
	OrderedCards []apiDashcard `json:"ordered_cards"` // older versions
}

type apiDashcard struct {
	CardID            *int `json:"card_id"`
	TabID             *int `json:"dashboard_tab_id"`
	Row               int  `json:"row"`
	Col               int  `json:"col"`
	SizeX             int  `json:"size_x"`
	SizeY             int  `json:"size_y"`
	ParameterMappings []struct {
		ParameterID string          `json:"parameter_id"`
		Target      json.RawMessage `json:"target"`
	} `json:"parameter_mappings"`
	Viz struct {
		Text        *string `json:"text"`
		CardTitle   string  `json:"card.title"`
		VirtualCard *struct {
			Display string `json:"display"`
		} `json:"virtual_card"`
	} `json:"visualization_settings"`
}

type apiSnippet struct {
	Name        string  `json:"name"`
	Description *string `json:"description"`
	Content     string  `json:"content"`
}

// ---------------------------------------------------------------- helpers

func decodeList[T any](raw json.RawMessage) ([]T, error) {
	s := strings.TrimSpace(string(raw))
	if strings.HasPrefix(s, "{") {
		var wrapped struct {
			Data []T `json:"data"`
		}
		err := json.Unmarshal(raw, &wrapped)
		return wrapped.Data, err
	}
	var list []T
	err := json.Unmarshal(raw, &list)
	return list, err
}

func intID(raw json.RawMessage) (int, bool) {
	var n int
	if json.Unmarshal(raw, &n) == nil {
		return n, true
	}
	return 0, false
}

func intOr(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}

func compactJSON(raw []byte) string {
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return string(raw)
	}
	b, _ := json.Marshal(v)
	return string(b)
}
