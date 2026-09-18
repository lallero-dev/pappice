package store

import (
	"errors"
	"path/filepath"
	"slices"
	"testing"
)

func TestProductMembershipLifecycle(t *testing.T) {
	tracker := openTestStore(t, filepath.Join(t.TempDir(), "tracker.db"))
	_, err := tracker.CreateFirstAdmin(CreateUser{Password: "correct horse", Email: "admin@example.test"})
	if err != nil {
		t.Fatal(err)
	}
	user, err := tracker.CreateUser(CreateUser{Password: "correct horse", Email: "bob@example.test", Role: "customer"})
	if err != nil {
		t.Fatal(err)
	}
	product, err := tracker.CreateProduct(CreateProduct{Key: "OPS", Name: "Operations"})
	if err != nil {
		t.Fatalf("create product: %v", err)
	}
	product, err = tracker.UpdateProduct(product.ID, UpdateProduct{Name: new("Operations Desk")})
	if err != nil {
		t.Fatalf("update product: %v", err)
	}
	if product.Name != "Operations Desk" {
		t.Fatalf("product = %#v", product)
	}
	if _, err := tracker.UpsertProductMember(product.ID, UpsertProductMember{UserID: user.ID, Role: "customer"}); err != nil {
		t.Fatalf("upsert member: %v", err)
	}
	if role, err := tracker.ProductRole(user.ID, product.ID); err != nil || role != "customer" {
		t.Fatalf("product role = %q err=%v", role, err)
	}
	if err := tracker.DeleteProductMember(product.ID, user.ID, EventContext{}); err != nil {
		t.Fatalf("delete member: %v", err)
	}
	if _, err := tracker.ProductRole(user.ID, product.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted product member error = %v, want ErrNotFound", err)
	}
}

func TestDeleteProductCascadesAndReportsOrphanedStorageKeys(t *testing.T) {
	tracker := openTestStore(t, filepath.Join(t.TempDir(), "tracker.db"))
	admin, err := tracker.CreateFirstAdmin(CreateUser{Email: "admin@example.test", Password: "correct horse"})
	if err != nil {
		t.Fatalf("create first admin: %v", err)
	}
	deleteProductID := mustListProducts(t, tracker, admin)[0].ID
	keepProduct, err := tracker.CreateProduct(CreateProduct{Key: "KEEP", Name: "Keep"})
	if err != nil {
		t.Fatalf("create keep product: %v", err)
	}
	shared := CreateAttachment{
		Filename:    "shared.txt",
		ContentType: "text/plain",
		SizeBytes:   6,
		SHA256:      "shared-hash",
		StorageKey:  "sh/ar/shared-hash",
	}
	orphan := CreateAttachment{
		Filename:    "orphan.txt",
		ContentType: "text/plain",
		SizeBytes:   6,
		SHA256:      "orphan-hash",
		StorageKey:  "or/ph/orphan-hash",
	}
	deletedTicket, err := tracker.CreateTicketWithAttachments(CreateTicket{
		ProductID:   deleteProductID,
		Title:       "Delete product ticket",
		ActorUserID: admin.ID,
	}, []CreateAttachment{shared, orphan})
	if err != nil {
		t.Fatalf("create deleted product ticket: %v", err)
	}
	keptTicket, err := tracker.CreateTicketWithAttachments(CreateTicket{
		ProductID:   keepProduct.ID,
		Title:       "Keep product ticket",
		ActorUserID: admin.ID,
	}, []CreateAttachment{shared})
	if err != nil {
		t.Fatalf("create kept product ticket: %v", err)
	}

	orphaned, err := tracker.DeleteProduct(deleteProductID, EventContext{})
	if err != nil {
		t.Fatalf("delete product: %v", err)
	}
	if slices.Contains(orphaned, shared.StorageKey) || !slices.Contains(orphaned, orphan.StorageKey) {
		t.Fatalf("orphaned storage keys = %#v", orphaned)
	}
	if _, err := tracker.GetTicket(deletedTicket.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted product ticket err = %v, want not found", err)
	}
	kept, err := tracker.GetTicket(keptTicket.ID)
	if err != nil {
		t.Fatalf("get kept ticket: %v", err)
	}
	if len(kept.Attachments) != 1 || kept.Attachments[0].StorageKey != shared.StorageKey {
		t.Fatalf("kept ticket attachments = %#v", kept.Attachments)
	}
}

func TestProductMembershipFiltersTickets(t *testing.T) {
	tracker := openTestStore(t, filepath.Join(t.TempDir(), "tracker.db"))
	admin, err := tracker.CreateFirstAdmin(CreateUser{Email: "admin@example.test", Password: "correct horse"})
	if err != nil {
		t.Fatalf("create admin: %v", err)
	}
	visibleProduct := mustListProducts(t, tracker, admin)[0]
	hiddenProduct, err := tracker.CreateProduct(CreateProduct{Key: "OPS", Name: "Operations"})
	if err != nil {
		t.Fatalf("create product: %v", err)
	}
	user, err := tracker.CreateUser(CreateUser{Email: "bob@example.test", Password: "correct horse"})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	if _, err := tracker.UpsertProductMember(visibleProduct.ID, UpsertProductMember{UserID: user.ID, Role: "viewer"}); err != nil {
		t.Fatalf("add member: %v", err)
	}
	if _, err := tracker.CreateTicket(CreateTicket{ProductID: visibleProduct.ID, Title: "Forbidden", ActorUserID: user.ID}); !errors.Is(err, ErrValidation) {
		t.Fatalf("viewer ticket error = %v, want ErrValidation", err)
	}
	if _, err := tracker.CreateTicket(CreateTicket{ProductID: visibleProduct.ID, Title: "Visible", ActorUserID: admin.ID}); err != nil {
		t.Fatalf("create visible ticket: %v", err)
	}
	if _, err := tracker.CreateTicket(CreateTicket{ProductID: hiddenProduct.ID, Title: "Hidden", ActorUserID: admin.ID}); err != nil {
		t.Fatalf("create hidden ticket: %v", err)
	}

	page, err := tracker.ListTicketSummariesPage(user, TicketSummaryFilter{})
	if err != nil {
		t.Fatalf("list visible tickets: %v", err)
	}
	if len(page.Tickets) != 1 || page.Tickets[0].Title != "Visible" {
		t.Fatalf("visible tickets = %#v", page.Tickets)
	}
	products := mustListProducts(t, tracker, user)
	if len(products) != 1 || products[0].ID != visibleProduct.ID {
		t.Fatalf("visible products = %#v", products)
	}
}
