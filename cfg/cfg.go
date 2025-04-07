package cfg

type (
	App struct {
		Name    string
		Version string
	}

	Mysql struct {
		Host                  string
		Port                  string
		Username              string
		Password              string
		Database              string
		MaxIdleConnection     int
		MaxOpenConnection     int
		MaxLifeTimeConnection int
	}

	GithubApi struct {
		AccessToken string
		ApiUrl      string
	}
)

type Config struct {
	App       App
	Mysql     Mysql
	GithubApi GithubApi
}
