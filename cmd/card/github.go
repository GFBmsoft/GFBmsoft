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

type repo struct {
	Name   string
	Stars  int
	IsFork bool
	Owner  string
}

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

// rest faz um GET na API REST; sem token usa o limite anônimo (60 req/h), suficiente para poucos repos.
func rest(token, path string) (int, []byte, error) {
	req, _ := http.NewRequest("GET", "https://api.github.com"+path, nil)
	req.Header.Set("Accept", "application/vnd.github+json")
	if token != "" {
		req.Header.Set("Authorization", "bearer "+token)
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	return resp.StatusCode, data, err
}

func fetchStats(token, login string) (Stats, error) {
	var st Stats
	var repos []repo
	var err error
	if token != "" {
		repos, err = graphqlProfile(token, login, &st)
	} else {
		repos, err = restProfile(login, &st)
	}
	if err != nil {
		return st, err
	}

	for _, r := range repos {
		if strings.EqualFold(r.Owner, login) {
			st.Repos++
			st.Stars += r.Stars
		}
		if r.IsFork {
			continue
		}
		commits, add, del, err := repoContrib(token, r.Name, login)
		if err != nil {
			return st, fmt.Errorf("%s: %w", r.Name, err)
		}
		st.Commits += commits
		st.Additions += add
		st.Deletions += del
	}
	return st, nil
}

// graphqlProfile lista repos próprios e de colaboração (inclui privados se o token permitir).
func graphqlProfile(token, login string, st *Stats) ([]repo, error) {
	var repos []repo
	var cursor *string
	for {
		var page struct {
			User struct {
				Followers                 struct{ TotalCount int } `json:"followers"`
				RepositoriesContributedTo struct{ TotalCount int } `json:"repositoriesContributedTo"`
				Repositories              struct {
					Nodes []struct {
						NameWithOwner  string `json:"nameWithOwner"`
						StargazerCount int    `json:"stargazerCount"`
						IsFork         bool   `json:"isFork"`
						Owner          struct {
							Login string `json:"login"`
						} `json:"owner"`
					} `json:"nodes"`
					PageInfo struct {
						HasNextPage bool   `json:"hasNextPage"`
						EndCursor   string `json:"endCursor"`
					} `json:"pageInfo"`
				} `json:"repositories"`
			} `json:"user"`
		}
		err := graphql(token, `query($login: String!, $cursor: String) {
			user(login: $login) {
				followers { totalCount }
				repositoriesContributedTo(contributionTypes: [COMMIT, PULL_REQUEST]) { totalCount }
				repositories(first: 100, after: $cursor, ownerAffiliations: [OWNER, COLLABORATOR, ORGANIZATION_MEMBER]) {
					nodes { nameWithOwner stargazerCount isFork owner { login } }
					pageInfo { hasNextPage endCursor }
				}
			}
		}`, map[string]any{"login": login, "cursor": cursor}, &page)
		if err != nil {
			return nil, err
		}
		st.Followers = page.User.Followers.TotalCount
		st.Contributed = page.User.RepositoriesContributedTo.TotalCount
		for _, n := range page.User.Repositories.Nodes {
			repos = append(repos, repo{n.NameWithOwner, n.StargazerCount, n.IsFork, n.Owner.Login})
		}
		if !page.User.Repositories.PageInfo.HasNextPage {
			return repos, nil
		}
		c := page.User.Repositories.PageInfo.EndCursor
		cursor = &c
	}
}

// restProfile usa só dados públicos, para rodar localmente sem token.
func restProfile(login string, st *Stats) ([]repo, error) {
	code, data, err := rest("", "/users/"+login)
	if err != nil {
		return nil, err
	}
	if code != http.StatusOK {
		return nil, fmt.Errorf("users %d: %s", code, data)
	}
	var user struct {
		Followers int `json:"followers"`
	}
	if err := json.Unmarshal(data, &user); err != nil {
		return nil, err
	}
	st.Followers = user.Followers

	var repos []repo
	for page := 1; ; page++ {
		code, data, err := rest("", fmt.Sprintf("/users/%s/repos?per_page=100&page=%d", login, page))
		if err != nil {
			return nil, err
		}
		if code != http.StatusOK {
			return nil, fmt.Errorf("repos %d: %s", code, data)
		}
		var list []struct {
			FullName string `json:"full_name"`
			Stars    int    `json:"stargazers_count"`
			Fork     bool   `json:"fork"`
			Owner    struct {
				Login string `json:"login"`
			} `json:"owner"`
		}
		if err := json.Unmarshal(data, &list); err != nil {
			return nil, err
		}
		for _, r := range list {
			repos = append(repos, repo{r.FullName, r.Stars, r.Fork, r.Owner.Login})
		}
		if len(list) < 100 {
			return repos, nil
		}
	}
}

// repoContrib soma commits, adições e remoções do usuário via /stats/contributors (branch padrão).
// A API responde 202 enquanto calcula as estatísticas, então tenta de novo algumas vezes.
func repoContrib(token, name, login string) (commits, add, del int, err error) {
	for attempt := 0; attempt < 8; attempt++ {
		code, data, err := rest(token, "/repos/"+name+"/stats/contributors")
		if err != nil {
			return 0, 0, 0, err
		}
		switch code {
		case http.StatusAccepted:
			time.Sleep(3 * time.Second)
			continue
		case http.StatusNoContent, http.StatusNotFound, http.StatusConflict: // repo vazio ou sem acesso
			return 0, 0, 0, nil
		case http.StatusOK:
		default:
			return 0, 0, 0, fmt.Errorf("stats %d: %s", code, data)
		}

		var contributors []struct {
			Total  int `json:"total"`
			Author struct {
				Login string `json:"login"`
			} `json:"author"`
			Weeks []struct {
				A int `json:"a"`
				D int `json:"d"`
			} `json:"weeks"`
		}
		if err := json.Unmarshal(data, &contributors); err != nil {
			return 0, 0, 0, err
		}
		for _, c := range contributors {
			if strings.EqualFold(c.Author.Login, login) {
				commits += c.Total
				for _, w := range c.Weeks {
					add += w.A
					del += w.D
				}
			}
		}
		return commits, add, del, nil
	}
	return 0, 0, 0, fmt.Errorf("estatísticas ainda em cálculo no GitHub")
}
