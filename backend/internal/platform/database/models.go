package database

import (
	"encoding/json"
	"time"

	"shop-mall/backend/internal/platform/uid"

	"github.com/google/uuid"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

type Role struct {
	ID          uid.ID       `gorm:"primaryKey;column:id"`
	Name        string       `gorm:"column:name;size:32;uniqueIndex"`
	Remark      string       `gorm:"column:remark;size:128"`
	Permissions []Permission `gorm:"many2many:role_permissions;joinForeignKey:RoleID;joinReferences:PermissionID"`
}

func (Role) TableName() string { return "roles" }

type Permission struct {
	ID   uid.ID `gorm:"primaryKey;column:id"`
	Code string `gorm:"column:code;size:64;uniqueIndex"`
	Name string `gorm:"column:name;size:64"`
}

func (Permission) TableName() string { return "permissions" }

// RolePermission is the join row between roles and permissions. It backs the
// composite writes in CreateRole/UpdateRole where the permission set is
// replaced wholesale.
type RolePermission struct {
	RoleID       uid.ID `gorm:"column:role_id;primaryKey"`
	PermissionID uid.ID `gorm:"column:permission_id;primaryKey"`
}

func (RolePermission) TableName() string { return "role_permissions" }

type AdminUser struct {
	ID           uid.ID     `gorm:"primaryKey;column:id"`
	Username     string     `gorm:"column:username;size:32;uniqueIndex"`
	PasswordHash string     `gorm:"column:password_hash;size:255"`
	TokenVersion int64      `gorm:"column:token_version"`
	RoleID       uid.ID     `gorm:"column:role_id"`
	Role         Role       `gorm:"foreignKey:RoleID;references:ID"`
	Enabled      bool       `gorm:"column:enabled"`
	LastLoginAt  *time.Time `gorm:"column:last_login_at"`
	CreatedAt    time.Time  `gorm:"column:created_at"`
	UpdatedAt    time.Time  `gorm:"column:updated_at"`
}

func (AdminUser) TableName() string { return "admin_users" }

type User struct {
	ID            uid.ID    `gorm:"primaryKey;column:id"`
	OpenID        string    `gorm:"column:openid;size:64;uniqueIndex"`
	Nickname      string    `gorm:"column:nickname;size:64"`
	AvatarURL     string    `gorm:"column:avatar_url"`
	PointsBalance int64     `gorm:"column:points_balance"`
	CreatedAt     time.Time `gorm:"column:created_at"`
	UpdatedAt     time.Time `gorm:"column:updated_at"`
}

func (User) TableName() string { return "users" }

type ProductStatus string

const (
	ProductStatusOnSale  ProductStatus = "on_sale"
	ProductStatusOffSale ProductStatus = "off_sale"
)

type Product struct {
	ID          uid.ID `gorm:"primaryKey;column:id"`
	Name        string `gorm:"column:name;size:128"`
	Description string `gorm:"column:description"`
	// Category is a stable key from the product package's category catalog;
	// empty means uncategorized.
	Category string `gorm:"column:category;size:32"`
	// Images is the ordered gallery of image object keys; the first entry is
	// the main image the public API also exposes as main_image.
	Images      datatypes.JSON `gorm:"column:images;type:jsonb;not null;default:'[]'"`
	PricePoints int64          `gorm:"column:price_points"`
	Stock       int32          `gorm:"column:stock"`
	Status      ProductStatus  `gorm:"column:status;size:16"`
	CreatedAt   time.Time      `gorm:"column:created_at"`
	UpdatedAt   time.Time      `gorm:"column:updated_at"`
}

func (Product) TableName() string { return "products" }

// ImageKeys decodes the images column. A malformed payload (which cannot be
// written through the service validation) degrades to an empty gallery.
func (p Product) ImageKeys() []string {
	var keys []string
	if len(p.Images) == 0 {
		return keys
	}
	if err := json.Unmarshal(p.Images, &keys); err != nil {
		return nil
	}
	return keys
}

// MainImageKey is the first gallery entry, or "" for an imageless product.
func (p Product) MainImageKey() string {
	keys := p.ImageKeys()
	if len(keys) == 0 {
		return ""
	}
	return keys[0]
}

type Address struct {
	ID        uid.ID    `gorm:"primaryKey;column:id"`
	UserID    uid.ID    `gorm:"column:user_id;index"`
	Receiver  string    `gorm:"column:receiver;size:32"`
	Phone     string    `gorm:"column:phone;size:20"`
	Region    string    `gorm:"column:region;size:64"`
	Detail    string    `gorm:"column:detail;size:255"`
	IsDefault bool      `gorm:"column:is_default"`
	Version   int64     `gorm:"column:version"`
	CreatedAt time.Time `gorm:"column:created_at"`
	UpdatedAt time.Time `gorm:"column:updated_at"`
}

func (Address) TableName() string { return "user_addresses" }

type CartItem struct {
	ID        uid.ID    `gorm:"primaryKey;column:id"`
	UserID    uid.ID    `gorm:"column:user_id;index:idx_cart_items_user_product,unique,priority:1"`
	ProductID uid.ID    `gorm:"column:product_id;index:idx_cart_items_user_product,unique,priority:2"`
	Quantity  int32     `gorm:"column:quantity"`
	Product   Product   `gorm:"foreignKey:ProductID;references:ID"`
	CreatedAt time.Time `gorm:"column:created_at"`
	UpdatedAt time.Time `gorm:"column:updated_at"`
}

func (CartItem) TableName() string { return "cart_items" }

type OrderStatus string

const (
	OrderStatusPaid            OrderStatus = "paid"
	OrderStatusShipped         OrderStatus = "shipped"
	OrderStatusCompleted       OrderStatus = "completed"
	OrderStatusRefundRequested OrderStatus = "refund_requested"
	OrderStatusRefunded        OrderStatus = "refunded"
)

type Order struct {
	ID                 uid.ID      `gorm:"primaryKey;column:id"`
	OrderNo            string      `gorm:"column:order_no;size:32;uniqueIndex"`
	UserID             uid.ID      `gorm:"column:user_id;index:idx_orders_user_status,priority:1"`
	ClientToken        string      `gorm:"column:client_token;size:64;index:idx_orders_user_client,unique,priority:2"`
	RequestHash        string      `gorm:"column:request_hash;size:64"`
	Status             OrderStatus `gorm:"column:status;size:20;index:idx_orders_user_status,priority:2;index:idx_orders_status_created,priority:1"`
	TotalPoints        int64       `gorm:"column:total_points"`
	Receiver           string      `gorm:"column:receiver;size:32"`
	Phone              string      `gorm:"column:phone;size:20"`
	Address            string      `gorm:"column:address;size:512"`
	RefundReason       string      `gorm:"column:refund_reason;size:255"`
	RefundRejectReason string      `gorm:"column:refund_reject_reason;size:255"`
	RefundReviewedByID *uid.ID     `gorm:"column:refund_reviewed_by"`
	RefundReviewedBy   *AdminUser  `gorm:"foreignKey:RefundReviewedByID;references:ID"`
	RefundReviewedAt   *time.Time  `gorm:"column:refund_reviewed_at"`
	PaidAt             time.Time   `gorm:"column:paid_at"`
	ShippedByID        *uid.ID     `gorm:"column:shipped_by"`
	ShippedBy          *AdminUser  `gorm:"foreignKey:ShippedByID;references:ID"`
	ShippedAt          *time.Time  `gorm:"column:shipped_at"`
	CompletedAt        *time.Time  `gorm:"column:completed_at"`
	RefundedAt         *time.Time  `gorm:"column:refunded_at"`
	CreatedAt          time.Time   `gorm:"column:created_at;index:idx_orders_user_status,priority:3;index:idx_orders_status_created,priority:2"`
	UpdatedAt          time.Time   `gorm:"column:updated_at"`
	Items              []OrderItem `gorm:"foreignKey:OrderID;references:ID"`
}

func (Order) TableName() string { return "orders" }

type OrderItem struct {
	ID            uid.ID  `gorm:"primaryKey;column:id"`
	OrderID       uid.ID  `gorm:"column:order_id;index"`
	ProductID     uid.ID  `gorm:"column:product_id"`
	ProductName   string  `gorm:"column:product_name;size:128"`
	ProductImage  string  `gorm:"column:product_image"`
	PriceSnapshot int64   `gorm:"column:price_snapshot"`
	Quantity      int32   `gorm:"column:quantity"`
	Product       Product `gorm:"foreignKey:ProductID;references:ID"`
}

func (OrderItem) TableName() string { return "order_items" }

type LedgerType string

const (
	LedgerTypeOrderPay    LedgerType = "order_pay"
	LedgerTypeOrderRefund LedgerType = "order_refund"
	LedgerTypeSignupBonus LedgerType = "signup_bonus"
	LedgerTypeAdminAdjust LedgerType = "admin_adjust"
)

type PointsLedger struct {
	ID               uid.ID     `gorm:"primaryKey;column:id"`
	UserID           uid.ID     `gorm:"column:user_id;index:idx_ledger_user_id,priority:1"`
	OrderID          *uid.ID    `gorm:"column:order_id;index"`
	EventKey         string     `gorm:"column:event_key;size:96;uniqueIndex"`
	RequestHash      *string    `gorm:"column:request_hash;size:64"`
	Type             LedgerType `gorm:"column:type;size:20"`
	Delta            int64      `gorm:"column:delta"`
	BalanceAfter     int64      `gorm:"column:balance_after"`
	CreatedByAdminID *uid.ID    `gorm:"column:created_by_admin_id"`
	Remark           string     `gorm:"column:remark;size:255"`
	CreatedAt        time.Time  `gorm:"column:created_at;index:idx_ledger_user_id,priority:2"`
	Order            *Order     `gorm:"foreignKey:OrderID;references:ID"`
	CreatedByAdmin   *AdminUser `gorm:"foreignKey:CreatedByAdminID;references:ID"`
}

func (PointsLedger) TableName() string { return "points_ledger" }

type AuditLog struct {
	ID           uid.ID         `gorm:"primaryKey;column:id"`
	ActorAdminID *uid.ID        `gorm:"column:actor_admin_id;index:idx_audit_actor_time,priority:1"`
	ActorRole    string         `gorm:"column:actor_role;size:32"`
	Action       string         `gorm:"column:action;size:64"`
	TargetType   string         `gorm:"column:target_type;size:64;index:idx_audit_target_time,priority:1"`
	TargetID     *uid.ID        `gorm:"column:target_id;index:idx_audit_target_time,priority:2"`
	Result       string         `gorm:"column:result;size:16"`
	BeforeData   datatypes.JSON `gorm:"column:before_data;type:jsonb"`
	AfterData    datatypes.JSON `gorm:"column:after_data;type:jsonb"`
	TraceID      string         `gorm:"column:trace_id;size:128"`
	CreatedAt    time.Time      `gorm:"column:created_at;index:idx_audit_actor_time,priority:2;index:idx_audit_target_time,priority:3"`
	ActorAdmin   *AdminUser     `gorm:"foreignKey:ActorAdminID;references:ID"`
}

func (AuditLog) TableName() string { return "audit_logs" }

// Every primary key is generated by the service layer before INSERT: the
// columns have no database default, so each model fills a zero ID with a
// fresh UUIDv7 on the way in. Callers that already carry an id (replay or
// composite paths) keep their value.

func (m *Role) BeforeCreate(*gorm.DB) error {
	return fillID(&m.ID)
}

func (m *Permission) BeforeCreate(*gorm.DB) error {
	return fillID(&m.ID)
}

func (m *AdminUser) BeforeCreate(*gorm.DB) error {
	return fillID(&m.ID)
}

func (m *User) BeforeCreate(*gorm.DB) error {
	return fillID(&m.ID)
}

func (m *Product) BeforeCreate(*gorm.DB) error {
	return fillID(&m.ID)
}

func (m *Address) BeforeCreate(*gorm.DB) error {
	return fillID(&m.ID)
}

func (m *CartItem) BeforeCreate(*gorm.DB) error {
	return fillID(&m.ID)
}

func (m *Order) BeforeCreate(*gorm.DB) error {
	return fillID(&m.ID)
}

func (m *OrderItem) BeforeCreate(*gorm.DB) error {
	return fillID(&m.ID)
}

func (m *PointsLedger) BeforeCreate(*gorm.DB) error {
	return fillID(&m.ID)
}

func (m *AuditLog) BeforeCreate(*gorm.DB) error {
	return fillID(&m.ID)
}

func (m *RolePermission) BeforeCreate(*gorm.DB) error {
	if err := fillID(&m.RoleID); err != nil {
		return err
	}
	return fillID(&m.PermissionID)
}

func fillID(target *uid.ID) error {
	if *target == uuid.Nil {
		*target = uid.New()
	}
	return nil
}
