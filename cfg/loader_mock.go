package cfg

type MockLoader struct{}

func NewMockLoader() (*MockLoader, error) {
	return &MockLoader{}, nil
}

func (yl *MockLoader) Load() (*Config, error) {
	return &Config{
		// App
		App: App{
			Name:    "Github star crawler",
			Version: "0.0.1",
		},

		// Mysql
		Mysql: Mysql{
			Host:                  "127.0.0.1",
			Password:              "root",
			Username:              "root",
			Port:                  "3306",
			Database:              "github_stars_v1",
			MaxIdleConnection:     10,
			MaxOpenConnection:     100,
			MaxLifeTimeConnection: 3600,
		},

		// GithubApi
		GithubApi: GithubApi{
			// AccessToken: "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",
			AccessToken: "",
			ApiUrl: 	"https://api.github.com/search/repositories?q=stars:>1&sort=stars&order=desc",
		},
	}, nil
}
