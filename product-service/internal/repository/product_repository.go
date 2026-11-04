package repository

import (
	"errors"
	"product-service/internal/models"

	"gorm.io/gorm"
)

var (
	ErrProductNotFound    = errors.New("product not found")
	ErrInsufficientStock  = errors.New("insufficient stock")
)

type ProductRepository struct {
	db *gorm.DB
}

func NewProductRepository(db *gorm.DB) *ProductRepository {
	return &ProductRepository{db: db}
}

func (r *ProductRepository) Create(product *models.Product) error {
	return r.db.Create(product).Error
}

func (r *ProductRepository) FindByID(id uint) (*models.Product, error) {
	var product models.Product
	if err := r.db.First(&product, id).Error; err != nil {
		return nil, err
	}
	return &product, nil
}

func (r *ProductRepository) FindAll() ([]models.Product, error) {
	var products []models.Product
	err := r.db.Find(&products).Error
	return products, err
}

// Update persists all fields of the given product. Returns only error.
func (r *ProductRepository) Update(product *models.Product) error {
	return r.db.Save(product).Error
}

// ReserveStock atomically decrements the stock of the given product by the
// given quantity. The UPDATE is guarded by a WHERE clause that ensures the
// current stock is >= the requested quantity, so concurrent orders cannot
// oversell. Returns the remaining stock after the decrement, or an error if
// the product doesn't exist or has insufficient stock.
func (r *ProductRepository) ReserveStock(productID uint, quantity int) (int, error) {
	result := r.db.Model(&models.Product{}).
		Where("id = ? AND stock >= ?", productID, quantity).
		Update("stock", gorm.Expr("stock - ?", quantity))

	if result.Error != nil {
		return 0, result.Error
	}
	if result.RowsAffected == 0 {
		// Either the product doesn't exist or stock < quantity.
		// Distinguish by checking if the product exists at all.
		var product models.Product
		err := r.db.Select("stock").First(&product, productID).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, ErrProductNotFound
		}
		if err != nil {
			return 0, err
		}
		// Product exists but stock < quantity.
		return int(product.Stock), ErrInsufficientStock
	}

	// Fetch the updated stock value.
	var product models.Product
	if err := r.db.Select("stock").First(&product, productID).Error; err != nil {
		return 0, err
	}
	return int(product.Stock), nil
}

func (r *ProductRepository) Delete(id uint) error {
	return r.db.Delete(&models.Product{}, id).Error
}

// ReleaseStock atomically increments the stock of the given product by the
// given quantity. This is the rollback counterpart of ReserveStock — it
// restores stock that was reserved but no longer needed (e.g. order creation
// failed after some items were already reserved). Returns the remaining stock
// after the increment, or an error if the product doesn't exist.
func (r *ProductRepository) ReleaseStock(productID uint, quantity int) (int, error) {
	result := r.db.Model(&models.Product{}).
		Where("id = ?", productID).
		Update("stock", gorm.Expr("stock + ?", quantity))

	if result.Error != nil {
		return 0, result.Error
	}
	if result.RowsAffected == 0 {
		return 0, ErrProductNotFound
	}

	// Fetch the updated stock value.
	var product models.Product
	if err := r.db.Select("stock").First(&product, productID).Error; err != nil {
		return 0, err
	}
	return int(product.Stock), nil
}
