package controller

import (
	"github.com/harderthanitneedstobe/rest-api/v0/pkg/models"
	"github.com/kataras/iris/v12"
	"github.com/kataras/iris/v12/context"
	"net/http"
	"strings"
)

func (c *Controller) handleBankAccounts(p iris.Party) {
	p.Get("/", c.getBankAccounts)

	// Create bank accounts manually.
	p.Post("/", c.postBankAccounts)
}

// List All Bank Accounts
// @id list-all-bank-accounts
// @description List's all of the bank accounts for the currently authenticated user.
// @Security ApiKeyAuth
// @Router /bank_accounts [get]
// @Success 200 {array} models.BankAccount
func (c *Controller) getBankAccounts(ctx *context.Context) {
	repo := c.mustGetAuthenticatedRepository(ctx)

	bankAccounts, err := repo.GetBankAccounts()
	if err != nil {
		c.wrapPgError(ctx, err, "failed to retrieve bank accounts")
		return
	}

	ctx.JSON(bankAccounts)
}

// Create Bank Account
// @id create-bank-account
// @description Create a bank account for the provided link.
// @Security ApiKeyAuth
// @Router /bank_accounts [post]
// @Success 200 {object} models.BankAccount
func (c *Controller) postBankAccounts(ctx *context.Context) {
	var bankAccount models.BankAccount
	if err := ctx.ReadJSON(&bankAccount); err != nil {
		c.wrapAndReturnError(ctx, err, http.StatusBadRequest, "malformed JSON")
		return
	}

	if bankAccount.LinkId == 0 {
		c.returnError(ctx, http.StatusBadRequest, "link Id must be provided")
		return
	}

	bankAccount.BankAccountId = 0
	bankAccount.Name = strings.TrimSpace(bankAccount.Name)
	bankAccount.Mask = strings.TrimSpace(bankAccount.Mask)

	// TODO (elliotcourant) Add proper bank account types that the user can specify. Make them required.
	bankAccount.Type = strings.TrimSpace(bankAccount.Type)
	bankAccount.SubType = strings.TrimSpace(bankAccount.SubType)

	if bankAccount.Name == "" {
		c.returnError(ctx, http.StatusBadRequest, "bank account must have a name")
		return
	}

	repo := c.mustGetAuthenticatedRepository(ctx)

	// Bank accounts can only be created this way when they are associated with a link that allows manual
	// management. If the link they specified does not, then a bank account cannot be created for this link.
	isManual, err := repo.GetLinkIsManual(bankAccount.LinkId)
	if err != nil {
		c.wrapPgError(ctx, err, "could not validate link is manual")
		return
	}

	if !isManual {
		c.returnError(ctx, http.StatusBadRequest, "cannot create a bank account for a non-manual link")
		return
	}

	if err := repo.CreateBankAccounts(bankAccount); err != nil {
		c.wrapPgError(ctx, err, "could not create bank account")
		return
	}

	ctx.JSON(bankAccount)
}
