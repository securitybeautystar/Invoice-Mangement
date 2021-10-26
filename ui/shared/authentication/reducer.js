import { ACTIVATE_SUBSCRIPTION, BOOTSTRAP_LOGIN, LOGOUT } from 'shared/authentication/actions';
import AuthenticationState from 'shared/authentication/state';
import * as Sentry from '@sentry/browser';

export default function reducer(state = new AuthenticationState(), action) {
  switch (action.type) {
    case BOOTSTRAP_LOGIN:
      // If the user is null then we are not logged in, just return the state.
      if (action.payload.user) {
        const accountId = action.payload.user.accountId.toString(10);
        Sentry.setUser({
          id: accountId,
          username: `account:${ accountId }`
        });
      }

      return {
        ...state,
        ...action.payload,
      };
    case ACTIVATE_SUBSCRIPTION:
      return {
        ...state,
        isActive: true,
      };
    case LOGOUT:
      Sentry.configureScope(scope => scope.setUser(null));
      return {
        ...state,
        isAuthenticated: false,
        token: null,
        user: null,
      };
    default:
      return state;
  }
}
