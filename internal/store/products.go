package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

func (s *Store) CreateProduct(input CreateProduct) (Product, error) {
	now := time.Now().UTC()
	tx, err := s.db.Begin()
	if err != nil {
		return Product{}, err
	}
	defer tx.Rollback()
	product, err := createProductTx(tx, input)
	if err != nil {
		return Product{}, err
	}
	if err := insertAppEventTx(tx, now, input.Event, "product.created", "product", product.ID, product.Key, map[string]any{"name": product.Name}, nil); err != nil {
		return Product{}, err
	}
	if err := tx.Commit(); err != nil {
		return Product{}, err
	}
	return product, nil
}

func (s *Store) ListProducts(user User) []Product {
	var rows *sql.Rows
	var err error
	if normalizeGlobalRole(user.Role) == "admin" {
		rows, err = s.db.Query(`
			SELECT id, key, name, description, 'manager', created_at, updated_at
			FROM products
			ORDER BY key`)
	} else {
		rows, err = s.db.Query(`
			SELECT p.id, p.key, p.name, p.description, pm.role, p.created_at, p.updated_at
			FROM products p
			JOIN product_members pm ON pm.product_id = p.id
			WHERE pm.user_id = ?
			ORDER BY p.key`,
			user.ID,
		)
	}
	if err != nil {
		return nil
	}
	defer rows.Close()

	var products []Product
	for rows.Next() {
		product, err := scanProduct(rows)
		if err == nil {
			products = append(products, product)
		}
	}
	return products
}

func (s *Store) GetProduct(id int64) (Product, error) {
	row := s.db.QueryRow(`
		SELECT id, key, name, description, '', created_at, updated_at
		FROM products
		WHERE id = ?`, id)
	product, err := scanProduct(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Product{}, ErrNotFound
	}
	return product, err
}

func (s *Store) UpdateProduct(id int64, patch UpdateProduct) (Product, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return Product{}, err
	}
	defer tx.Rollback()
	product, err := getProductTx(tx, id)
	if err != nil {
		return Product{}, err
	}
	if patch.Name != nil {
		product.Name = strings.TrimSpace(*patch.Name)
		if product.Name == "" {
			return Product{}, fmt.Errorf("%w: product name is required", ErrValidation)
		}
	}
	if patch.Description != nil {
		product.Description = strings.TrimSpace(*patch.Description)
	}
	product.UpdatedAt = time.Now().UTC()
	_, err = tx.Exec(
		`UPDATE products SET name = ?, description = ?, updated_at = ? WHERE id = ?`,
		product.Name, product.Description, formatTime(product.UpdatedAt), id,
	)
	if err != nil {
		return Product{}, err
	}
	if err := insertAppEventTx(tx, product.UpdatedAt, patch.Event, "product.updated", "product", product.ID, product.Key, map[string]any{"name": product.Name}, nil); err != nil {
		return Product{}, err
	}
	if err := tx.Commit(); err != nil {
		return Product{}, err
	}
	return s.GetProduct(id)
}

func (s *Store) DeleteProduct(id int64, event ...EventContext) ([]string, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	product, err := getProductTx(tx, id)
	if err != nil {
		return nil, err
	}
	storageKeys, err := productAttachmentStorageKeysTx(tx, id)
	if err != nil {
		return nil, err
	}
	result, err := tx.Exec(`DELETE FROM products WHERE id = ?`, id)
	if err != nil {
		return nil, err
	}
	if changed, _ := result.RowsAffected(); changed == 0 {
		return nil, ErrNotFound
	}
	orphaned, err := orphanedAttachmentStorageKeysTx(tx, storageKeys)
	if err != nil {
		return nil, err
	}
	if err := insertAppEventTx(tx, time.Now().UTC(), firstEventContext(event), "product.deleted", "product", product.ID, product.Key, map[string]any{"name": product.Name}, nil); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return orphaned, nil
}

func (s *Store) ProductRole(userID, productID int64) (string, bool) {
	var role string
	err := s.db.QueryRow(`SELECT role FROM product_members WHERE user_id = ? AND product_id = ?`, userID, productID).Scan(&role)
	return normalizeProductRole(role), err == nil
}

func (s *Store) ListProductMembers(productID int64) []ProductMember {
	rows, err := s.db.Query(`
		SELECT pm.product_id, u.id, u.email, u.display_name, pm.role, pm.created_at
		FROM product_members pm
		JOIN users u ON u.id = pm.user_id
		WHERE pm.product_id = ?
		ORDER BY pm.role, u.email`, productID)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var members []ProductMember
	for rows.Next() {
		var member ProductMember
		var created string
		if err := rows.Scan(&member.ProductID, &member.UserID, &member.Email, &member.DisplayName, &member.Role, &created); err == nil {
			member.Role = normalizeProductRole(member.Role)
			member.CreatedAt = parseTime(created)
			members = append(members, member)
		}
	}
	return members
}

func (s *Store) UpsertProductMember(productID int64, input UpsertProductMember) (ProductMember, error) {
	role := normalizeProductRole(input.Role)
	if !isValid(validProductRoles, role) {
		return ProductMember{}, fmt.Errorf("%w: invalid product role %q", ErrValidation, role)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return ProductMember{}, err
	}
	defer tx.Rollback()
	if _, err := getProductTx(tx, productID); err != nil {
		return ProductMember{}, err
	}
	user, err := getUserTx(tx, input.UserID)
	if err != nil {
		return ProductMember{}, err
	}
	if user.Disabled {
		return ProductMember{}, fmt.Errorf("%w: user is disabled", ErrValidation)
	}
	now := time.Now().UTC()
	_, err = tx.Exec(`
		INSERT INTO product_members (product_id, user_id, role, created_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(product_id, user_id) DO UPDATE SET role = excluded.role`,
		productID, input.UserID, role, formatTime(now),
	)
	if err != nil {
		return ProductMember{}, err
	}
	member := ProductMember{
		ProductID:   productID,
		UserID:      user.ID,
		Email:       user.Email,
		DisplayName: user.DisplayName,
		Role:        role,
		CreatedAt:   now,
	}
	if err := insertAppEventTx(tx, now, input.Event, "product_member.upserted", "user", member.UserID, member.Email, map[string]any{
		"product_id": productID,
		"role":       member.Role,
	}, nil); err != nil {
		return ProductMember{}, err
	}
	if err := tx.Commit(); err != nil {
		return ProductMember{}, err
	}
	for _, listed := range s.ListProductMembers(productID) {
		if listed.UserID == input.UserID {
			return listed, nil
		}
	}
	return member, nil
}

func (s *Store) DeleteProductMember(productID, userID int64, event ...EventContext) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	user, _ := getUserTx(tx, userID)
	result, err := tx.Exec(`DELETE FROM product_members WHERE product_id = ? AND user_id = ?`, productID, userID)
	if err != nil {
		return err
	}
	if changed, _ := result.RowsAffected(); changed == 0 {
		return ErrNotFound
	}
	targetName := user.Email
	if targetName == "" {
		targetName = strconv.FormatInt(userID, 10)
	}
	if err := insertAppEventTx(tx, time.Now().UTC(), firstEventContext(event), "product_member.removed", "user", userID, targetName, map[string]any{"product_id": productID}, nil); err != nil {
		return err
	}
	return tx.Commit()
}

func createProductTx(tx *sql.Tx, input CreateProduct) (Product, error) {
	key := strings.ToUpper(strings.TrimSpace(input.Key))
	if !productKeyPattern.MatchString(key) {
		return Product{}, fmt.Errorf("%w: product key must be 2-16 uppercase letters or numbers", ErrValidation)
	}
	name := defaultString(input.Name, key)
	description := strings.TrimSpace(input.Description)
	now := time.Now().UTC()
	result, err := tx.Exec(
		`INSERT INTO products (key, name, description, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`,
		key, name, description, formatTime(now), formatTime(now),
	)
	if err != nil {
		return Product{}, normalizeSQLError(err)
	}
	id, _ := result.LastInsertId()
	return Product{
		ID:          id,
		Key:         key,
		Name:        name,
		Description: description,
		CreatedAt:   now,
		UpdatedAt:   now,
	}, nil
}

func getProductTx(tx *sql.Tx, id int64) (Product, error) {
	row := tx.QueryRow(`
		SELECT id, key, name, description, '', created_at, updated_at
		FROM products WHERE id = ?`, id)
	product, err := scanProduct(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Product{}, ErrNotFound
	}
	return product, err
}

func scanProduct(rows scanner) (Product, error) {
	var product Product
	var created, updated string
	if err := rows.Scan(
		&product.ID, &product.Key, &product.Name, &product.Description, &product.Role,
		&created, &updated,
	); err != nil {
		return Product{}, err
	}
	product.Role = normalizeProductRole(product.Role)
	product.CreatedAt = parseTime(created)
	product.UpdatedAt = parseTime(updated)
	return product, nil
}
