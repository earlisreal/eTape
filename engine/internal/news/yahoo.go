package news

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/earlisreal/eTape/engine/internal/session"
)

const yahooSearchEndpoint = "https://query1.finance.yahoo.com/v1/finance/search"

type yahooFetcher struct {
	client   *http.Client
	endpoint string
	maxCount int
}

type yahooSearchResponse struct {
	News []yahooNews `json:"news"`
}

type yahooNews struct {
	Title               string   `json:"title"`
	Publisher           string   `json:"publisher"`
	Link                string   `json:"link"`
	ProviderPublishTime int64    `json:"providerPublishTime"`
	RelatedTickers      []string `json:"relatedTickers"`
}

func (f yahooFetcher) fetch(ctx context.Context, symbol string) ([]searchNews, error) {
	normalized, ok := normalizeMoomooSecurity(symbol)
	if !ok {
		return nil, nil
	}
	u, err := url.Parse(f.endpoint)
	if err != nil {
		return nil, err
	}
	query := u.Query()
	query.Set("q", strings.TrimPrefix(normalized, "US."))
	query.Set("newsCount", strconv.Itoa(f.maxCount))
	u.RawQuery = query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "eTape/experimental-news")
	resp, err := f.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("yahoo news: %s", resp.Status)
	}
	var body yahooSearchResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&body); err != nil {
		return nil, err
	}
	out := make([]searchNews, 0, len(body.News))
	for _, item := range body.News {
		if strings.TrimSpace(item.Title) == "" {
			continue
		}
		related := make([]string, 0, len(item.RelatedTickers))
		for _, ticker := range item.RelatedTickers {
			if security, ok := normalizeMoomooSecurity("US." + ticker); ok {
				related = append(related, security)
			}
		}
		source := "Yahoo Finance"
		if publisher := strings.TrimSpace(item.Publisher); publisher != "" {
			source += " / " + publisher
		}
		published := ""
		if item.ProviderPublishTime > 0 {
			published = time.Unix(item.ProviderPublishTime, 0).In(session.Loc()).Format("2006-01-02 15:04:05")
		}
		out = append(out, searchNews{Title: item.Title, Source: source, URL: item.Link, PublishTime: published, RelatedSecurities: related})
	}
	return out, nil
}
