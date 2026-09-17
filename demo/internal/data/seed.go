// Package data provides the sample accounts and tickets used by both demos.
package data

import (
	"fmt"

	"pappice/internal/store"
)

const Password = "pappice-demo"

type Accounts struct {
	Admin    string
	Staff    string
	Customer string
	Password string
}

// Seed populates an empty store with sample accounts, a product, and tickets.
func Seed(tracker *store.Store) (Accounts, error) {
	admin, err := tracker.CreateFirstAdmin(store.CreateUser{
		DisplayName: "Alex Admin",
		Email:       "admin@example.test",
		Password:    Password,
	})
	if err != nil {
		return Accounts{}, err
	}
	staff, err := tracker.CreateUser(store.CreateUser{
		DisplayName: "Sam Staff",
		Email:       "staff@example.test",
		Password:    Password,
		Role:        "staff",
	})
	if err != nil {
		return Accounts{}, err
	}
	customer, err := tracker.CreateUser(store.CreateUser{
		DisplayName: "Casey Customer",
		Email:       "customer@example.test",
		Password:    Password,
		Role:        "customer",
	})
	if err != nil {
		return Accounts{}, err
	}

	products, err := tracker.ListProducts(admin)
	if err != nil {
		return Accounts{}, err
	}
	if len(products) == 0 {
		return Accounts{}, fmt.Errorf("default product was not created")
	}
	defaultProductID := products[0].ID
	product, err := tracker.CreateProduct(store.CreateProduct{
		Key:         "WEB",
		Name:        "Website Support",
		Description: "Example customer support product.",
	})
	if err != nil {
		return Accounts{}, err
	}
	if _, err := tracker.DeleteProduct(defaultProductID, store.EventContext{}); err != nil {
		return Accounts{}, err
	}
	for _, member := range []struct {
		user store.User
		role string
	}{
		{staff, "staff"},
		{customer, "customer"},
	} {
		if _, err := tracker.UpsertProductMember(product.ID, store.UpsertProductMember{
			UserID: member.user.ID,
			Role:   member.role,
		}); err != nil {
			return Accounts{}, err
		}
	}

	ticket, err := tracker.CreateTicket(store.CreateTicket{
		ProductID:      product.ID,
		Title:          "Login page returns an error",
		Description:    "I cannot sign in after the last password reset. The page shows an error and sends me back to the login form.",
		Priority:       "high",
		AssigneeUserID: staff.ID,
		ActorUserID:    customer.ID,
	})
	if err != nil {
		return Accounts{}, err
	}
	if _, err := tracker.SaveTicket(store.SaveTicketInput{
		TicketID: ticket.ID,
		Comment: &store.AddComment{
			Body:       "Thanks, I can reproduce it. I am checking the account setup now.",
			Visibility: "public",
		},
		ActorUserID: staff.ID,
	}); err != nil {
		return Accounts{}, err
	}
	if _, err := tracker.SaveTicket(store.SaveTicketInput{
		TicketID: ticket.ID,
		Comment: &store.AddComment{
			Body:       "I tried again from a private window and it still fails.",
			Visibility: "public",
		},
		ActorUserID: customer.ID,
	}); err != nil {
		return Accounts{}, err
	}

	for _, example := range []struct {
		title       string
		description string
		priority    string
	}{
		{"Invoice download link is expired", "The invoice link in last week's email no longer opens.", "normal"},
		{"Mobile menu covers account settings", "The account settings are hidden behind the navigation menu on my phone.", "normal"},
		{"Webhook delivery is delayed", "Order notifications are reaching our integration several minutes late.", "high"},
		{"Cannot update billing address", "Saving a new billing address returns an error without changing the account.", "high"},
		{"Question about team permissions", "Which role should we use for colleagues who only need to view requests?", "low"},
		{"Export contains duplicate rows", "The latest CSV export contains some invoices more than once.", "normal"},
	} {
		if _, err := tracker.CreateTicket(store.CreateTicket{
			ProductID:   product.ID,
			Title:       example.title,
			Description: example.description,
			Priority:    example.priority,
			ActorUserID: customer.ID,
		}); err != nil {
			return Accounts{}, err
		}
	}

	return Accounts{
		Admin:    admin.Email,
		Staff:    staff.Email,
		Customer: customer.Email,
		Password: Password,
	}, nil
}
