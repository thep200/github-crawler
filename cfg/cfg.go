package cfg

type (
	App struct {
		Name    string `yaml:"name" mapstructure:"name"`
		Version string `yaml:"version" mapstructure:"version"`
	}

	Mysql struct {
		Host                  string `yaml:"host" mapstructure:"host"`
		Port                  string `yaml:"port" mapstructure:"port"`
		Username              string `yaml:"username" mapstructure:"username"`
		Password              string `yaml:"password" mapstructure:"password"`
		Database              string `yaml:"database" mapstructure:"database"`
		MaxIdleConnection     int    `yaml:"max_idle_connection" mapstructure:"max_idle_connection"`
		MaxOpenConnection     int    `yaml:"max_open_connection" mapstructure:"max_open_connection"`
		MaxLifeTimeConnection int    `yaml:"max_life_time_connection" mapstructure:"max_life_time_connection"`
	}

	GithubApi struct {
		AccessToken       string `yaml:"access_token" mapstructure:"access_token"`
		ApiUrl            string `yaml:"api_url" mapstructure:"api_url"`
		ReleasesApiUrl    string `yaml:"releases_api_url" mapstructure:"releases_api_url"` // Template URL for releases API
		CommitsApiUrl     string `yaml:"commits_api_url" mapstructure:"commits_api_url"`   // Template URL for commits API
		RequestsPerSecond int    `yaml:"requests_per_second" mapstructure:"requests_per_second"`
		ThrottleDelay     int    `yaml:"throttle_delay" mapstructure:"throttle_delay"` // Milliseconds
	}
)

type Config struct {
	App       App       `yaml:"app" mapstructure:"app"`
	Mysql     Mysql     `yaml:"mysql" mapstructure:"mysql"`
	GithubApi GithubApi `yaml:"github_api" mapstructure:"github_api"`
}
