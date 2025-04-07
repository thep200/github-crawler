package crawlinfo

type Info struct {
	ID           string `json:"id" gorm:"id"`
	Name         string `json:"name" gorm:"name"`
	NumberOfStar int    `json:"number_of_stars" gorm:"number_of_stars"`
}
