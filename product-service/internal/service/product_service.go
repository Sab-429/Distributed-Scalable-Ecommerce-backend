package service

import (
	"context"
	"errors"
	"fmt"
	"product-service/internal/cache"
	"product-service/internal/models"
	"product-service/internal/repository"
)

type ProductService struct {
	repo  *repository.ProductRepository
	cache *cache.ProductCache
}

func NewProductService(
	repo *repository.ProductRepository,
	cache *cache.ProductCache,
) *ProductService {
	return &ProductService{repo: repo, cache: cache}
}

func (s *ProductService) CreateProduct(
	product *models.Product,
) error {
	if product.Name == "" {
		return errors.New("product name is required")
	}
	if product.Price <= 0 {
		return errors.New("price must be greater than zero")
	}
	if product.Stock < 0 {
		return errors.New("stock cannot be negative")
	}

	return s.repo.Create(product)
}

func (s *ProductService) GetProduct(id uint) (*models.Product, error) {
	return s.repo.FindByID(id)
}

func (s *ProductService) GetProducts() ([]models.Product, error) {
	return s.repo.FindAll()
}

func (s *ProductService) UpdateProduct(
	ctx context.Context,
	product *models.Product,
) error {
	if product.Name == "" {
		return errors.New("product name is required")
	}
	if product.Price <= 0 {
		return errors.New("price must be greater than zero")
	}

	if err := s.repo.Update(product); err != nil {
		return err
	}

	// Invalidate the cache entry so the next read is fresh.
	_ = s.cache.Delete(ctx, product.ID)
	return nil
}

func (s *ProductService) DeleteProduct(ctx context.Context, id uint) error {
	// Remove from the DB first, then evict from cache.
	if err := s.repo.Delete(id); err != nil {
		return err
	}

	_ = s.cache.Delete(ctx, id)
	return nil
}

// GetByID checks the cache before hitting the database.
func (s *ProductService) GetByID(ctx context.Context, id uint) (*models.Product, error) {
	product, err := s.cache.Get(ctx, id)
	if err != nil {
		// Log cache error but fall through to DB.
		fmt.Printf("cache get error for product %d: %v\n", id, err)
	}

	if product != nil {
		fmt.Println("CACHE HIT")
		return product, nil
	}

	fmt.Println("CACHE MISS")

	product, err = s.repo.FindByID(id)
	if err != nil {
		return nil, err
	}

	_ = s.cache.Set(ctx, product)
	return product, nil
}
