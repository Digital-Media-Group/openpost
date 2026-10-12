### Added

- Add a `client` workspace role. A client may connect and reconnect the social accounts the workspace publishes through, and may decide a parked approval, but may not author or edit content and may not administer the workspace. It is the role for the person who owns the accounts and the reputation without working inside the product.

- Expose the capability separately from edit rights. A workspace summary now reports `can_connect` alongside `can_edit`, and the account-connection controls follow it, so a client reaches the connect buttons that the role exists for while remaining unable to change content.

- Land an invited client on the account-connection screen after accepting the invitation, so the single emailed link takes them to the buttons they came to press instead of the workspace home.
