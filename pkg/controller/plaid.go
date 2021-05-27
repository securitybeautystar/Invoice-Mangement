package controller

import (
	"context"
	"fmt"
	"github.com/getsentry/sentry-go"
	"github.com/kataras/iris/v12"
	"github.com/monetrapp/rest-api/pkg/models"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"net/http"
	"strconv"
	"time"

	"github.com/kataras/iris/v12/core/router"
	"github.com/plaid/plaid-go/plaid"
)

func (c *Controller) handlePlaidLinkEndpoints(p router.Party) {
	p.Get("/token/new", c.newPlaidToken)
	p.Put("/update/{linkId:uint64}", c.updatePlaidLink)
	p.Post("/token/callback", c.plaidTokenCallback)
	p.Post("/update/callback", c.updatePlaidTokenCallback)
	p.Get("/setup/wait/{linkId:uint64}", c.waitForPlaid)
}

func (c *Controller) storeLinkTokenInCache(ctx context.Context, log *logrus.Entry, userId uint64, linkToken string, expiration time.Time) error {
	span := sentry.StartSpan(ctx, "StoreLinkTokenInCache")
	defer span.Finish()

	cache, err := c.cache.GetContext(ctx)
	if err != nil {
		log.WithError(err).Warn("failed to get cache connection")
		return errors.Wrap(err, "failed to get cache connection")
	}
	defer cache.Close()

	key := fmt.Sprintf("plaidInProgress_%d", userId)
	return errors.Wrap(cache.Send("SET", key, linkToken, "EXAT", expiration.Unix()), "failed to cache link token")
}

// New Plaid Token
// @Summary New Plaid Token
// @id new-plaid-token
// @tags Plaid
// @description Generates a link token from Plaid to be used to authenticate a user's bank account with our application.
// @Security ApiKeyAuth
// @Produce json
// @Router /plaid/token/new [get]
// @Param use_cache query bool false "If true, the API will check and see if a plaid link token already exists for the current user. If one is present then it is returned instead of creating a new link token."
// @Success 200 {object} swag.PlaidNewLinkTokenResponse
// @Failure 500 {object} ApiError Something went wrong on our end.
func (c *Controller) newPlaidToken(ctx iris.Context) {
	// Retrieve the user's details. We need to pass some of these along to
	// plaid as part of the linking process.
	me, err := c.mustGetAuthenticatedRepository(ctx).GetMe()
	if err != nil {
		c.wrapAndReturnError(ctx, err, http.StatusInternalServerError, "failed to get user details for link")
		return
	}

	userId := c.mustGetUserId(ctx)

	log := c.log.WithFields(logrus.Fields{
		"accountId": me.AccountId,
		"userId":    me.UserId,
		"loginId":   me.LoginId,
	})

	checkCacheForLinkToken := func(ctx context.Context) (linkToken string, _ error) {
		span := sentry.StartSpan(ctx, "CheckCacheForLinkToken")
		defer span.Finish()

		cache, err := c.cache.GetContext(ctx)
		if err != nil {
			log.WithError(err).Warn("failed to get cache connection")
			return "", errors.Wrap(err, "failed to get cache connection")
		}
		defer cache.Close()

		// Check and see if there is already a plaid link in progress for the current user.
		result, err := cache.Do("GET", fmt.Sprintf("plaidInProgress_%d", me.UserId))
		if err != nil {
			log.WithError(err).Warn("failed to retrieve link token from cache")
			return "", errors.Wrap(err, "failed to retrieve link token from cache")
		}

		switch actual := result.(type) {
		case string:
			return actual, nil
		case *string:
			if actual != nil {
				return *actual, nil
			}
		case []byte:
			return string(actual), nil
		}

		return "", nil
	}

	if checkCache, err := ctx.URLParamBool("use_cache"); err == nil && checkCache {
		if linkToken, err := checkCacheForLinkToken(c.getContext(ctx)); err == nil && len(linkToken) > 0 {
			log.Info("successfully found existing link token in cache")
			ctx.JSON(map[string]interface{}{
				"linkToken": linkToken,
			})
			return
		}
	}

	plaidProducts := []string{
		"transactions",
	}

	legalName := ""
	if len(me.LastName) > 0 {
		legalName = fmt.Sprintf("%s %s", me.FirstName, me.LastName)
	}

	var phoneNumber string
	if me.Login.PhoneNumber != nil {
		phoneNumber = me.Login.PhoneNumber.E164()
	}

	var webhook string
	if c.configuration.Plaid.WebhooksEnabled {
		domain := c.configuration.Plaid.WebhooksDomain
		if domain != "" {
			webhook = fmt.Sprintf("%s/plaid/webhook", c.configuration.Plaid.WebhooksDomain)
		} else {
			c.log.Errorf("plaid webhooks are enabled, but they cannot be registered with without a domain")
		}
	}

	redirectUri := fmt.Sprintf("https://%s/plaid/oauth-return", c.configuration.UIDomainName)

	token, err := c.plaid.CreateLinkToken(c.getContext(ctx), plaid.LinkTokenConfigs{
		User: &plaid.LinkTokenUser{
			ClientUserID: strconv.FormatUint(userId, 10),
			LegalName:    legalName,
			PhoneNumber:  phoneNumber,
			EmailAddress: me.Login.Email,
			// TODO (elliotcourant) I'm going to leave these be for now but we need
			//  to loop back and add this once email/phone verification is working.
			PhoneNumberVerifiedTime:  time.Time{},
			EmailAddressVerifiedTime: time.Time{},
		},
		ClientName: "monetr",
		Products:   plaidProducts,
		CountryCodes: []string{
			"US",
		},
		Webhook:               webhook,
		AccountFilters:        nil,
		CrossAppItemAdd:       nil,
		PaymentInitiation:     nil,
		Language:              "en",
		LinkCustomizationName: "",
		RedirectUri:           redirectUri,
	})
	if err != nil {
		c.wrapAndReturnError(ctx, err, http.StatusInternalServerError, "failed to create link token")
		return
	}

	if err = c.storeLinkTokenInCache(c.getContext(ctx), log, me.UserId, token.LinkToken, token.Expiration); err != nil {
		log.WithError(err).Warn("failed to cache link token")
	}

	ctx.JSON(map[string]interface{}{
		"linkToken": token.LinkToken,
	})
}

// Update Plaid Link
// @Summary Update Plaid Link
// @id update-plaid-link
// @tags Plaid
// @description Update an existing Plaid link, this can be used to re-authenticate a link if it requires it or to potentially solve an error state.
// @Security ApiKeyAuth
// @Produce json
// @Router /plaid/update/{linkId:uint64} [put]
// @Param linkId path uint64 true "The Link Id that you wish to put into update mode, must be a Plaid link."
// @Success 200 {object} swag.PlaidNewLinkTokenResponse
// @Failure 500 {object} ApiError Something went wrong on our end.
func (c *Controller) updatePlaidLink(ctx iris.Context) {
	linkId := ctx.Params().GetUint64Default("linkId", 0)
	if linkId == 0 {
		c.badRequest(ctx, "must specify a link Id")
		return
	}

	log := c.getLog(ctx).WithField("linkId", linkId)

	// Retrieve the user's details. We need to pass some of these along to
	// plaid as part of the linking process.
	repo := c.mustGetAuthenticatedRepository(ctx)

	link, err := repo.GetLink(c.getContext(ctx), linkId)
	if err != nil {
		c.wrapPgError(ctx, err, "failed to retrieve link")
		return
	}

	if link.LinkType != models.PlaidLinkType {
		c.badRequest(ctx, "cannot update a non-Plaid link")
		return
	}

	if link.PlaidLink == nil {
		c.returnError(ctx, http.StatusInternalServerError, "no Plaid details associated with link")
		return
	}

	me, err := repo.GetMe()
	if err != nil {
		c.wrapPgError(ctx, err, "failed to retrieve user details")
		return
	}

	legalName := ""
	if len(me.LastName) > 0 {
		legalName = fmt.Sprintf("%s %s", me.FirstName, me.LastName)
	} else {
		// TODO Handle a missing last name, we need a legal name Plaid.
		//  Should this be considered an error state?
	}

	var phoneNumber string
	if me.Login.PhoneNumber != nil {
		phoneNumber = me.Login.PhoneNumber.E164()
	}

	var webhook string
	if c.configuration.Plaid.WebhooksEnabled {
		domain := c.configuration.Plaid.WebhooksDomain
		if domain != "" {
			webhook = fmt.Sprintf("%s/plaid/webhook", c.configuration.Plaid.WebhooksDomain)
		} else {
			log.Errorf("plaid webhooks are enabled, but they cannot be registered with without a domain")
		}
	}

	redirectUri := fmt.Sprintf("https://%s/plaid/oauth-return", c.configuration.UIDomainName)

	token, err := c.plaid.CreateLinkToken(c.getContext(ctx), plaid.LinkTokenConfigs{
		User: &plaid.LinkTokenUser{
			ClientUserID: strconv.FormatUint(me.UserId, 10),
			LegalName:    legalName,
			PhoneNumber:  phoneNumber,
			EmailAddress: me.Login.Email,
			// TODO Add in email/phone verification.
			PhoneNumberVerifiedTime:  time.Time{},
			EmailAddressVerifiedTime: time.Time{},
		},
		ClientName: "monetr",
		CountryCodes: []string{
			"US",
		},
		Webhook:               webhook,
		AccountFilters:        nil,
		CrossAppItemAdd:       nil,
		PaymentInitiation:     nil,
		Language:              "en",
		LinkCustomizationName: "",
		RedirectUri:           redirectUri,
		AccessToken:           link.PlaidLink.AccessToken,
	})
	if err != nil {
		c.wrapAndReturnError(ctx, err, http.StatusInternalServerError, "failed to create link token")
		return
	}

	if err = c.storeLinkTokenInCache(c.getContext(ctx), log, me.UserId, token.LinkToken, token.Expiration); err != nil {
		log.WithError(err).Warn("failed to cache link token")
	}

	ctx.JSON(map[string]interface{}{
		"linkToken": token.LinkToken,
	})
}

func (c *Controller) updatePlaidTokenCallback(ctx iris.Context) {
	var callbackRequest struct {
		LinkId      uint64 `json:"linkId"`
		PublicToken string `json:"publicToken"`
	}
	if err := ctx.ReadJSON(&callbackRequest); err != nil {
		c.wrapAndReturnError(ctx, err, http.StatusBadRequest, "malformed json")
		return
	}

	repo := c.mustGetAuthenticatedRepository(ctx)

	link, err := repo.GetLink(c.getContext(ctx), callbackRequest.LinkId)
	if err != nil {
		c.wrapPgError(ctx, err, "failed to retrieve link")
		return
	}

	result, err := c.plaid.ExchangePublicToken(c.getContext(ctx), callbackRequest.PublicToken)
	if err != nil {
		c.wrapAndReturnError(ctx, err, http.StatusInternalServerError, "failed to exchange token")
		return
	}

	log := c.getLog(ctx)

	if link.PlaidLink.AccessToken != result.AccessToken {
		log.Info("access token for link has been updated")
		link.PlaidLink.AccessToken = result.AccessToken
		if err = repo.UpdatePlaidLink(c.getContext(ctx), link.PlaidLink); err != nil {
			c.wrapPgError(ctx, err, "failed to update Plaid link")
			return
		}
	} else {
		log.Info("access token for link has not changed")
	}

	link.LinkStatus = models.LinkStatusSetup
	link.ErrorCode = nil
	if err = repo.UpdateLink(link); err != nil {
		c.wrapPgError(ctx, err, "failed to update link status")
		return
	}

	_, err = c.job.TriggerPullLatestTransactions(link.AccountId, link.LinkId, 0)
	if err != nil {
		log.WithError(err).Warn("failed to trigger pulling latest transactions after updating plaid link")
	}

	ctx.JSON(link)
}

// Plaid Token Callback
// @Summary Plaid Token Callback
// @id plaid-token-callback
// @tags Plaid
// @description Receives the public token after a user has authenticated their bank account to exchange with plaid.
// @Security ApiKeyAuth
// @Produce json
// @Router /plaid/token/callback [post]
// @Success 200 {object} swag.PlaidTokenCallbackResponse
// @Failure 500 {object} ApiError Something went wrong on our end.
func (c *Controller) plaidTokenCallback(ctx iris.Context) {
	var callbackRequest struct {
		PublicToken     string   `json:"publicToken"`
		InstitutionId   string   `json:"institutionId"`
		InstitutionName string   `json:"institutionName"`
		AccountIds      []string `json:"accountIds"`
	}
	if err := ctx.ReadJSON(&callbackRequest); err != nil {
		c.wrapAndReturnError(ctx, err, http.StatusBadRequest, "malformed json")
		return
	}

	if len(callbackRequest.AccountIds) == 0 {
		c.returnError(ctx, http.StatusBadRequest, "must select at least one account")
		return
	}

	result, err := c.plaid.ExchangePublicToken(c.getContext(ctx), callbackRequest.PublicToken)
	if err != nil {
		c.wrapAndReturnError(ctx, err, http.StatusInternalServerError, "failed to exchange token")
		return
	}

	plaidAccounts, err := c.plaid.GetAccounts(c.getContext(ctx), result.AccessToken, plaid.GetAccountsOptions{
		AccountIDs: callbackRequest.AccountIds,
	})
	if err != nil {
		c.wrapAndReturnError(ctx, err, http.StatusInternalServerError, "failed to retrieve accounts")
		return
	}

	if len(plaidAccounts) == 0 {
		c.returnError(ctx, http.StatusInternalServerError, "could not retrieve details for any accounts")
		return
	}

	repo := c.mustGetAuthenticatedRepository(ctx)

	var webhook string
	if c.configuration.Plaid.WebhooksEnabled {
		domain := c.configuration.Plaid.WebhooksDomain
		if domain != "" {
			webhook = fmt.Sprintf("%s/plaid/webhook", c.configuration.Plaid.WebhooksDomain)
		} else {
			c.log.Errorf("plaid webhooks are enabled, but they cannot be registered with without a domain")
		}
	}

	plaidLink := models.PlaidLink{
		ItemId:      result.ItemID,
		AccessToken: result.AccessToken,
		Products: []string{
			// TODO (elliotcourant) Make this based on what product's we sent in the create link token request.
			"transactions",
		},
		WebhookUrl:      webhook,
		InstitutionId:   callbackRequest.InstitutionId,
		InstitutionName: callbackRequest.InstitutionName,
	}
	if err := repo.CreatePlaidLink(&plaidLink); err != nil {
		c.wrapAndReturnError(ctx, err, http.StatusInternalServerError, "failed to store credentials")
		return
	}

	link := models.Link{
		AccountId:       repo.AccountId(),
		PlaidLinkId:     &plaidLink.PlaidLinkID,
		LinkType:        models.PlaidLinkType,
		LinkStatus:      models.LinkStatusPending,
		InstitutionName: callbackRequest.InstitutionName,
		CreatedByUserId: repo.UserId(),
	}
	if err = repo.CreateLink(&link); err != nil {
		c.wrapAndReturnError(ctx, err, http.StatusInternalServerError, "failed to create link")
		return
	}

	now := time.Now().UTC()
	accounts := make([]models.BankAccount, len(plaidAccounts))
	for i, plaidAccount := range plaidAccounts {
		accounts[i] = models.BankAccount{
			AccountId:         repo.AccountId(),
			LinkId:            link.LinkId,
			PlaidAccountId:    plaidAccount.AccountID,
			AvailableBalance:  int64(plaidAccount.Balances.Available * 100),
			CurrentBalance:    int64(plaidAccount.Balances.Current * 100),
			Name:              plaidAccount.Name,
			Mask:              plaidAccount.Mask,
			PlaidName:         plaidAccount.Name,
			PlaidOfficialName: plaidAccount.OfficialName,
			Type:              plaidAccount.Type,
			SubType:           plaidAccount.Subtype,
			LastUpdated:       now,
		}
	}
	if err = repo.CreateBankAccounts(accounts...); err != nil {
		c.wrapAndReturnError(ctx, err, http.StatusInternalServerError, "failed to create bank accounts")
		return
	}

	var jobIdStr *string
	if !c.configuration.Plaid.WebhooksEnabled {
		jobId, err := c.job.TriggerPullInitialTransactions(link.AccountId, link.CreatedByUserId, link.LinkId)
		if err != nil {
			c.wrapAndReturnError(ctx, err, http.StatusInternalServerError, "failed to pull initial transactions")
			return
		}

		jobIdStr = &jobId
	}

	ctx.JSON(map[string]interface{}{
		"success": true,
		"linkId":  link.LinkId,
		"jobId":   jobIdStr,
	})
}

// Wait For Plaid Account Data
// @Summary Wait For Plaid Account Data
// @id wait-for-plaid-data
// @tags Plaid
// @description Long poll endpoint that will timeout if data has not yet been pulled. Or will return 200 if data is ready.
// @Security ApiKeyAuth
// @Param linkId path string true "Link ID for the plaid link that is being setup. NOTE: Not Plaid's ID, this is a numeric ID we assign to the object that is returned from the callback endpoint."
// @Router /plaid/link/setup/wait/{linkId:uint64} [get]
// @Success 200
// @Success 408
func (c *Controller) waitForPlaid(ctx iris.Context) {
	// TODO Make the waitForPlaid endpoint handle both linkId and jobId.
	linkId := ctx.Params().GetUint64Default("linkId", 0)
	if linkId == 0 {
		c.badRequest(ctx, "must specify a job Id")
		return
	}

	log := c.log.WithFields(logrus.Fields{
		"accountId": c.mustGetAccountId(ctx),
		"linkId":    linkId,
	})

	repo := c.mustGetAuthenticatedRepository(ctx)
	link, err := repo.GetLink(c.getContext(ctx), linkId)
	if err != nil {
		c.wrapPgError(ctx, err, "failed to retrieve link")
		return
	}

	// If the link is done just return.
	if link.LinkStatus == models.LinkStatusSetup {
		return
	}

	channelName := fmt.Sprintf("initial_plaid_link_%d_%d", c.mustGetAccountId(ctx), linkId)

	listener, err := c.ps.Subscribe(c.getContext(ctx), channelName)
	if err != nil {
		c.wrapPgError(ctx, err, "failed to listen on channel")
		return
	}
	defer func() {
		if err = listener.Close(); err != nil {
			log.WithFields(logrus.Fields{
				"accountId": c.mustGetAccountId(ctx),
				"linkId":    linkId,
			}).WithError(err).Error("failed to gracefully close listener")
		}
	}()

	log.Debugf("waiting for link to be setup on channel: %s", channelName)

	deadLine := time.NewTimer(30 * time.Second)
	defer deadLine.Stop()

	select {
	case <-deadLine.C:
		ctx.StatusCode(http.StatusRequestTimeout)
		log.Trace("timed out waiting for link to be setup")
		return
	case <-listener.Channel():
		// Just exit successfully, any message on this channel is considered a success.
		log.Trace("link setup successfully")
		return
	}
}
