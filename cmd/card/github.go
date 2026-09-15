package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

var client = &http.Client{Timeout: 30 * time.Second}

func graphql(token, query string, vars map[string]any, out any) error {
	body, _ := json.Marshal(map[string]any{"query": query, "variables": vars})
	req, _ := http.NewRequest("POST", "https://api.github.com/graphql", bytes.NewReader(body))
	req.Header.Set("Authorization", "bearer "+token)
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("graphql %d: %s", resp.StatusCode, data)
	}
	var env struct {
		Data   json.RawMessage `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(data, &env); err != nil {
		return err
	}
	if len(env.Errors) > 0 {
		return fmt.Errorf("graphql: %s", env.Errors[0].Message)
	}
	return json.Unmarshal(env.Data, out)
}

func fetchStats(token, login string) (Stats, error) {
	var st Stats

	var base struct {
		User struct {
			CreatedAt                 time.Time                `json:"createdAt"`
			Followers                 struct{ TotalCount int } `json:"followers"`
			RepositoriesContributedTo struct{ TotalCount int } `json:"repositoriesContributedTo"`
		} `json:"user"`
	}
	err := graphql(token, `query($login: String!) {
		user(login: $login) {
			createdAt
			followers { totalCount }
			repositoriesContributedTo(contributionTypes: [COMMIT, PULL_REQUEST]) { totalCount }
		}
	}`, map[string]any{"login": login}, &base)
	if err != nil {
		return st, err
	}
	st.Followers = base.User.Followers.TotalCount
	st.Contributed = base.User.RepositoriesContributedTo.TotalCount

	// Commits: o contributionsCollection aceita no máximo um ano por consulta.
	for from := base.User.CreatedAt; from.Before(time.Now()); from = from.AddDate(1, 0, 0) {
		var cc struct {
			User struct {
				ContributionsCollection struct {
					TotalCommitContributions int `json:"totalCommitContributions"`
				} `json:"contributionsCollection"`
			} `json:"user"`
		}
		err := graphql(token, `query($login: String!, $from: DateTime!, $to: DateTime!) {
			user(login: $login) { contributionsCollection(from: $from, to: $to) { totalCommitContributions } }
		}`, map[string]any{"login": login, "from": from, "to": from.AddDate(1, 0, 0)}, &cc)
		if err != nil {
			return st, err
		}
		st.Commits += cc.User.ContributionsCollection.TotalCommitContributions
	}

	// Repositórios próprios e de colaboração: estrelas e linhas de código.
	type repo struct {
		NameWithOwner  string `json:"nameWithOwner"`
		StargazerCount int    `json:"stargazerCount"`
		IsFork         bool   `json:"isFork"`
		Owner          struct {
			Login string `json:"login"`
		} `json:"owner"`
	}
	var repos []repo
	cursor := (*string)(nil)
	for {
		var page struct {
			User struct {
				Repositories struct {
					Nodes    []repo
					PageInfo struct {
						HasNextPage bool   `json:"hasNextPage"`
						EndCursor   string `json:"endCursor"`
					} `json:"pageInfo"`
				} `json:"repositories"`
			} `json:"user"`
		}
		err := graphql(token, `query($login: String!, $cursor: String) {
			user(login: $login) {
				repositories(first: 100, after: $cursor, ownerAffiliations: [OWNER, COLLABORATOR, ORGANIZATION_MEMBER]) {
					nodes { nameWithOwner stargazerCount isFork owner { login } }
					pageInfo { hasNextPage endCursor }
				}
			}
		}`, map[string]any{"login": login, "cursor": cursor}, &page)
		if err != nil {
			return st, err
		}
		repos = append(repos, page.User.Repositories.Nodes...)
		if !page.User.Repositories.PageInfo.HasNextPage {
			break
		}
		c := page.User.Repositories.PageInfo.EndCursor
		cursor = &c
	}

	for _, r := range repos {
		if strings.EqualFold(r.Owner.Login, login) {
			st.Repos++
			st.Stars += r.StargazerCount
		}
		if r.IsFork {
			continue
		}
		add, del, err := repoLines(token, r.NameWithOwner, login)
		if err != nil {
			return st, fmt.Errorf("%s: %w", r.NameWithOwner, err)
		}
		st.Additions += add
		st.Deletions += del
	}
	return st, nil
}

// repoLines soma adições e remoções do usuário via /stats/contributors.
// A API responde 202 enquanto calcula as estatísticas, então tenta de novo algumas vezes.
func repoLines(token, repo, login string) (add, del int, err error) {
	for attempt := 0; attempt < 8; attempt++ {
		req, _ := http.NewRequest("GET", "https://api.github.com/repos/"+repo+"/stats/contributors", nil)
		req.Header.Set("Authorization", "bearer "+token)
		resp, err := client.Do(req)
		if err != nil {
			return 0, 0, err
		}
		data, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		switch resp.StatusCode {
		case http.StatusAccepted:
			time.Sleep(3 * time.Second)
			continue
		case http.StatusNoContent, http.StatusNotFound, http.StatusConflict: // repo vazio ou sem acesso
			return 0, 0, nil
		case http.StatusOK:
		default:
			return 0, 0, fmt.Errorf("stats %d: %s", resp.StatusCode, data)
		}

		var contributors []struct {
			Author struct {
				Login string `json:"login"`
			} `json:"author"`
			Weeks []struct {
				A int `json:"a"`
				D int `json:"d"`
			} `json:"weeks"`
		}
		if err := json.Unmarshal(data, &contributors); err != nil {
			return 0, 0, err
		}
		for _, c := range contributors {
			if strings.EqualFold(c.Author.Login, login) {
				for _, w := range c.Weeks {
					add += w.A
					del += w.D
				}
			}
		}
		return add, del, nil
	}
	return 0, 0, nil // estatísticas ainda em cálculo; entram na próxima execução
}
