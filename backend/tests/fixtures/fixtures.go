package fixtures

type User struct {
	OpenID        string
	Nickname      string
	AvatarURL     string
	PointsBalance int64
}

type Product struct {
	SKU         string
	Name        string
	Description string
	MainImage   string
	PricePoints int64
	Stock       int32
	Status      string
}

type Address struct {
	UserOpenID string
	Receiver   string
	Phone      string
	Region     string
	Detail     string
	IsDefault  bool
	Version    int64
}

type Order struct {
	OrderNo     string
	UserOpenID  string
	ProductSKU  string
	ClientToken string
	RequestHash string
	Status      string
	TotalPoints int64
	Receiver    string
	Phone       string
	Address     string
}

type Administrator struct {
	Username     string
	PasswordHash string
	Role         string
	TokenVersion int64
	Enabled      bool
}

var Users = []User{
	{
		OpenID:        "fixture-user-openid",
		Nickname:      "Fixture Buyer",
		AvatarURL:     "https://cdn.example.test/fixtures/buyer.png",
		PointsBalance: 1000,
	},
}

var Products = []Product{
	{
		SKU:         "fixture-product-001",
		Name:        "Fixture Product",
		Description: "Deterministic product fixture",
		MainImage:   "products/fixture-product-001.jpg",
		PricePoints: 100,
		Stock:       10,
		Status:      "on_sale",
	},
}

var Addresses = []Address{
	{
		UserOpenID: "fixture-user-openid",
		Receiver:   "Fixture Receiver",
		Phone:      "13800138000",
		Region:     "北京市朝阳区",
		Detail:     "建国路 88 号测试地址",
		IsDefault:  true,
		Version:    1,
	},
}

var Orders = []Order{
	{
		OrderNo:     "FIXTURE-ORDER-001",
		UserOpenID:  "fixture-user-openid",
		ProductSKU:  "fixture-product-001",
		ClientToken: "fixture-client-token-001",
		RequestHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Status:      "paid",
		TotalPoints: 100,
		Receiver:    "Fixture Receiver",
		Phone:       "13800138000",
		Address:     "北京市朝阳区建国路 88 号测试地址",
	},
}

var Administrators = []Administrator{
	{
		Username:     "fixture-admin",
		PasswordHash: "$argon2id$v=19$m=65536,t=3,p=2$fixture-salt$fixture-hash",
		Role:         "super_admin",
		TokenVersion: 1,
		Enabled:      true,
	},
}

type FixtureSnapshot struct {
	Users          []User
	Products       []Product
	Addresses      []Address
	Orders         []Order
	Administrators []Administrator
}

func Snapshot() FixtureSnapshot {
	return FixtureSnapshot{
		Users:          append([]User(nil), Users...),
		Products:       append([]Product(nil), Products...),
		Addresses:      append([]Address(nil), Addresses...),
		Orders:         append([]Order(nil), Orders...),
		Administrators: append([]Administrator(nil), Administrators...),
	}
}
