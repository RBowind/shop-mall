package product

// Category catalogs the mall's product categories. Products store the stable
// key; the public categories endpoint pairs each key with its Chinese label
// and the live on-sale product count, so the mini program never hard-codes
// category names.
type Category string

const (
	CategoryDigital Category = "digital" // 数码
	CategoryHome    Category = "home"    // 家居
	CategoryBeauty  Category = "beauty"  // 美妆
	CategoryFood    Category = "food"    // 食品
	CategoryApparel Category = "apparel" // 服饰
)

// CategoryInfo is one entry of the public categories catalog.
type CategoryInfo struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	ProductCount int64  `json:"product_count"`
}

// categoryNames maps every catalog key to its display label.
var categoryNames = map[Category]string{
	CategoryDigital: "数码",
	CategoryHome:    "家居",
	CategoryBeauty:  "美妆",
	CategoryFood:    "食品",
	CategoryApparel: "服饰",
}

// CategoryCatalog returns every catalog entry in display order, with an
// optional per-category live product count. A nil counts map leaves
// ProductCount zero.
func CategoryCatalog(counts map[Category]int64) []CategoryInfo {
	order := []Category{CategoryDigital, CategoryHome, CategoryBeauty, CategoryFood, CategoryApparel}
	catalog := make([]CategoryInfo, 0, len(order))
	for _, key := range order {
		info := CategoryInfo{ID: string(key), Name: categoryNames[key]}
		if counts != nil {
			info.ProductCount = counts[key]
		}
		catalog = append(catalog, info)
	}
	return catalog
}

// validCategory reports whether value is empty (uncategorized) or a catalog
// key.
func validCategory(value string) bool {
	if value == "" {
		return true
	}
	_, ok := categoryNames[Category(value)]
	return ok
}
