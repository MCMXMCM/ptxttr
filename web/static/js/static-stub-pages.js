import { assetURL } from "./asset-paths.js";

function infoPageBrand() {
  return `
    <div class="about-page-brand">
      <span class="about-page-logo" aria-hidden="true">
        <img src="${assetURL("img/ascritch_icon_black.png")}" alt="" width="80" height="80" decoding="async" class="about-logo about-logo-light-scheme">
        <img src="${assetURL("img/ascritch_icon_white.png")}" alt="" width="80" height="80" decoding="async" class="about-logo about-logo-dark-scheme">
      </span>
      <span class="about-page-app-name">Plain Text Nostr</span>
    </div>
  `;
}

function infoPageFooter() {
  return `
    <nav class="rail-nav legal-page-links" aria-label="Related pages">
      <a href="/support" data-relay-aware data-main-menu-link><span class="rail-icon" aria-hidden="true">></span><span class="rail-label">Support</span></a>
      <a href="/ios-plain-text-nostr" data-relay-aware data-main-menu-link><span class="rail-icon" aria-hidden="true">></span><span class="rail-label">iOS app</span></a>
      <a href="/terms" data-relay-aware data-main-menu-link><span class="rail-icon" aria-hidden="true">></span><span class="rail-label">Terms of Service</span></a>
      <a href="/privacy" data-relay-aware data-main-menu-link><span class="rail-icon" aria-hidden="true">></span><span class="rail-label">Privacy Policy</span></a>
      <a href="/about" data-relay-aware data-main-menu-link><span class="rail-icon" aria-hidden="true">></span><span class="rail-label">About</span></a>
    </nav>
  `;
}

function aboutMainMarkup() {
  return `
    <section class="feed-column shell-main-top" data-shell-main>
      <section class="page-heading about-page">
        ${infoPageBrand()}
        <h1>About</h1>
        <p class="about-page-lead muted">A simple and fast Nostr web reader.</p>
        <p class="about-page-lead">On the web, your keys and signing stay in your browser. The server caches slices of the web of trust from your follow graph and keeps notes from those people, plus overlapping users, on a least-recently-used basis. That shared cache helps keep the web reader fast to share and affordable to run.</p>
        <p class="about-page-lead">If you want no app-server dependency and prefer to keep everything local, use the iOS app. It manages your data on-device and does not depend on the Plain Text Nostr web server.</p>

        <h2 class="about-page-sub">NIPs and libraries</h2>
        <nav class="rail-nav about-page-links" aria-label="Nostr specifications and dependencies">
          <a href="https://github.com/nostr-protocol/nips" rel="noopener noreferrer" target="_blank"><span class="rail-icon" aria-hidden="true">></span><span class="rail-label">Nostr NIPs (specs and implementations)</span></a>
          <a href="https://pkg.go.dev/fiatjaf.com/nostr" rel="noopener noreferrer" target="_blank"><span class="rail-icon" aria-hidden="true">></span><span class="rail-label">fiatjaf.com/nostr (events, nip19, relay types)</span></a>
        </nav>

        <h2 class="about-page-sub">iOS</h2>
        <nav class="rail-nav about-page-links" aria-label="Plain Text Nostr for iOS">
          <a href="/ios-plain-text-nostr" data-relay-aware data-main-menu-link><span class="rail-icon" aria-hidden="true">></span><span class="rail-label">Plain Text Nostr for iOS</span></a>
          <a href="https://testflight.apple.com/join/pz6ggn7D" rel="noopener noreferrer" target="_blank"><span class="rail-icon" aria-hidden="true">></span><span class="rail-label">Join the TestFlight beta</span></a>
        </nav>

        <h2 class="about-page-sub">Legal and support</h2>
        <nav class="rail-nav about-page-links" aria-label="Legal and support pages">
          <a href="/support" data-relay-aware data-main-menu-link><span class="rail-icon" aria-hidden="true">></span><span class="rail-label">Support</span></a>
          <a href="/privacy" data-relay-aware data-main-menu-link><span class="rail-icon" aria-hidden="true">></span><span class="rail-label">Privacy Policy</span></a>
          <a href="/terms" data-relay-aware data-main-menu-link><span class="rail-icon" aria-hidden="true">></span><span class="rail-label">Terms of Service</span></a>
        </nav>
      </section>
    </section>
  `;
}

function relayManagerPanelsMarkup() {
  return `
    <div class="settings-mode-tree-group">
      <div class="settings-mode-tree-group-main">
            <div class="settings-slider-tree settings-mode-tree-stacked">
              <div class="settings-mode-branch" aria-hidden="true"></div>
              <div class="settings-mode-row-head">
                <strong>Effective Read Relays</strong>
              </div>
              <div class="settings-mode-row-body">
                <ul class="relay-list" data-relay-insight-effective>
                  <li class="muted">No effective read relays.</li>
                </ul>
              </div>
            </div>
            <div class="settings-slider-tree settings-mode-tree-stacked">
              <div class="settings-mode-branch" aria-hidden="true"></div>
              <div class="settings-mode-row-head">
                <strong>Your Relays (NIP-65)</strong>
              </div>
              <div class="settings-mode-row-body">
                <p class="muted" data-relay-insight-status>Loading relay insight...</p>
                <ul class="relay-list" data-relay-insight-published>
                  <li class="muted">No published relay preferences yet.</li>
                </ul>
              </div>
            </div>
            <div class="settings-slider-tree settings-mode-tree-stacked settings-relay-advanced" hidden>
              <div class="settings-mode-branch" aria-hidden="true"></div>
              <div class="settings-mode-row-head">
                <strong>Effective Defaults</strong>
              </div>
              <div class="settings-mode-row-body">
                <ul class="relay-list" data-relay-insight-defaults>
                  <li class="muted">No effective defaults available yet.</li>
                </ul>
              </div>
            </div>
            <div class="settings-slider-tree settings-mode-tree-stacked settings-relay-advanced" hidden>
              <div class="settings-mode-branch" aria-hidden="true"></div>
              <div class="settings-mode-row-head">
                <strong>Discovered Relay Hints</strong>
              </div>
              <div class="settings-mode-row-body">
                <ul class="relay-list" data-relay-insight-discovered>
                  <li class="muted">No discovered relay hints yet.</li>
                </ul>
              </div>
            </div>
            <div class="settings-slider-tree settings-mode-tree-stacked settings-relay-advanced" hidden>
              <div class="settings-mode-branch" aria-hidden="true"></div>
              <div class="settings-mode-row-head">
                <strong>Backend Recommendations</strong>
              </div>
              <div class="settings-mode-row-body">
                <ul class="relay-list" data-relay-insight-recommended>
                  <li class="muted">No recommendations available yet.</li>
                </ul>
              </div>
            </div>
            <div class="settings-slider-tree settings-mode-tree-stacked">
              <div class="settings-mode-branch" aria-hidden="true"></div>
              <div class="settings-mode-row-head">
                <strong>Outbox Routing</strong>
              </div>
              <div class="settings-mode-row-body">
                <p class="muted" data-relay-outbox-summary>No outbox routing data yet.</p>
                <ul class="relay-list" data-relay-insight-outbox>
                  <li class="muted">No active outbox relays yet.</li>
                </ul>
              </div>
            </div>
            <div class="settings-slider-tree settings-mode-tree-stacked" data-relay-edit-section hidden>
              <div class="settings-mode-branch" aria-hidden="true"></div>
              <div class="settings-mode-row-head">
                <strong>Edit Relay List</strong>
              </div>
              <div class="settings-mode-row-body">
                <small class="muted">Write relays are where you publish. Read relays are where replies and reactions may be delivered.</small>
                <form class="settings-form" data-relay-preferences-form>
                  <label>Relay URL
                    <input type="text" name="relay" data-relay-preference-input placeholder="wss://relay.example.com">
                  </label>
                  <label>Usage
                    <select name="usage" data-relay-preference-usage>
                      <option value="any">Read + write</option>
                      <option value="write">Write only</option>
                      <option value="read">Read only</option>
                    </select>
                  </label>
                  <div class="toolbar dialog-actions">
                    <button type="button" data-relay-preference-add>Add relay</button>
                    <button type="button" data-relay-preference-fetch>Fetch relay list</button>
                    <button type="button" data-relay-preference-use-defaults>Use effective defaults</button>
                    <button type="button" data-relay-preference-use-recommended>Use recommendations</button>
                  </div>
                  <ul class="relay-list" data-relay-preferences-list>
                    <li class="muted">Add relay preferences here.</li>
                  </ul>
                  <div class="toolbar dialog-actions" data-relay-preferences-publish-actions hidden>
                    <button type="submit" data-relay-preferences-submit>Publish relay preferences</button>
                  </div>
                </form>
              </div>
            </div>
      </div>
    </div>
  `;
}

function relayManagerMarkup(options = {}) {
  const heading = options.heading || "Relays";
  const intro = options.intro || "Inspect effective relay routing and edit your published NIP-65 relay list when signed in.";
  return `
    <section class="page-heading">
      <h1>${heading}</h1>
      <p class="muted">${intro}</p>
    </section>
    <section class="settings-card">
      <div class="settings-form settings-preferences">
        ${relayManagerPanelsMarkup()}
      </div>
    </section>
  `;
}

function loginMainMarkup() {
  return `
    <section class="feed-column shell-main-top" data-shell-main>
      <section class="page-heading">
        <div class="ascii-border">+------------------------------------------------------------------------------+</div>
          <div class="ascii-content">
            <h1>Login</h1>
          </div>
        <div class="ascii-border">+------------------------------------------------------------------------------+</div>
      </section>

      <section class="login-intro">
        <p class="muted">Log in with an existing key or extension, or sign up to create a new keypair in your browser.</p>
      </section>

      <div class="login-split">
        <div class="login-split-column login-split-column--signin">
          <div class="user-tabs profile-tabs login-tabs">
            <nav class="user-tab-nav" aria-label="Login methods">
              <label class="user-tab-label" for="login-tab-readonly">Public Key</label>
              <span class="user-tab-sep" aria-hidden="true">·</span>
              <label class="user-tab-label" for="login-tab-nip07">Browser Extension</label>
              <span class="user-tab-sep" aria-hidden="true">·</span>
              <label class="user-tab-label" for="login-tab-yolo">Private Key</label>
            </nav>

            <input type="radio" name="login-tab" id="login-tab-readonly" class="user-tab-state" checked>
            <section class="user-tab-panel login-tab-panel" aria-label="Npub login">
              <h2>Npub Login (read-only)</h2>
              <p class="muted">Browse notes as any account using a public key. This does not enable signing or posting.</p>
              <form data-login-readonly class="login-tab-stacked-form">
                <input name="pubkey" data-pubkey-input placeholder="npub or hex public key" required>
                <div class="login-tab-panel-actions">
                  <button type="submit">Use public key</button>
                </div>
              </form>
            </section>

            <input type="radio" name="login-tab" id="login-tab-nip07" class="user-tab-state">
            <section class="user-tab-panel login-tab-panel" aria-label="Browser extension login">
              <h2>Browser Extension (NIP-07)</h2>
              <p class="muted">Use a browser extension that exposes <code>window.nostr</code> to connect your account.</p>
              <div class="login-tab-panel-actions">
                <button type="button" data-login-nip07>Connect extension</button>
              </div>
            </section>

            <input type="radio" name="login-tab" id="login-tab-yolo" class="user-tab-state">
            <section class="user-tab-panel login-tab-panel login-tab-panel-danger" aria-label="Nsec login">
              <h2>Nsec Login (Private key YOLO)</h2>
              <p class="muted">Dangerous: your private key is handled in browser JavaScript and stored in this browser so the account can persist across restarts and be switched locally.</p>
              <form data-login-yolo class="login-tab-stacked-form">
                <input name="secret" placeholder="nsec or hex private key" autocomplete="off">
                <div class="login-tab-panel-actions">
                  <button type="submit">Use login key</button>
                </div>
              </form>
            </section>
          </div>
        </div>

        <div class="login-split-column login-split-column--signup">
          <h2 class="login-split-heading">Sign up</h2>
          <section class="login-signup-panel login-tab-panel login-tab-panel-danger" aria-label="Sign up">
            <p class="muted" data-signup-intro>Create a new Nostr identity. Your browser generates a keypair, keeps it on this device for future logins, and never sends it anywhere until you choose to post.</p>
            <div class="login-tab-panel-actions" data-signup-intro-actions>
              <button type="button" data-signup-generate>Create account</button>
            </div>
            <div class="login-signup-credentials" data-signup-credentials hidden>
              <p class="muted"><strong>Save these now.</strong> Your private key (<code>nsec</code>) cannot be recovered if you lose it. Anyone with your <code>nsec</code> can post as you.</p>
              <div class="login-signup-secret-row">
                <label class="login-signup-field-label" for="signup-nsec-display">nsec (private)</label>
                <div class="login-signup-field-row">
                  <input id="signup-nsec-display" class="login-signup-secret-input" type="text" readonly autocomplete="off" spellcheck="false" data-signup-nsec-input>
                  <button type="button" data-signup-copy-nsec>Copy</button>
                </div>
              </div>
              <div class="login-signup-secret-row">
                <label class="login-signup-field-label" for="signup-npub-display">npub (public)</label>
                <div class="login-signup-field-row">
                  <input id="signup-npub-display" type="text" readonly autocomplete="off" spellcheck="false" data-signup-npub-input>
                  <button type="button" data-signup-copy-npub>Copy</button>
                </div>
              </div>
              <p class="login-signup-continue-wrap">
                <button type="button" data-signup-continue>Continue to feed</button>
              </p>
            </div>
          </section>
        </div>
      </div>

      <section class="session-panel">
        <div class="ascii-border">+------------------------------------------------------------------------------+</div>
          <h2>Login status</h2>
          <pre data-session-state>Not logged in.</pre>
          <p data-session-actions hidden>
            <a data-session-feed-link data-relay-aware href="/">Open feed</a>
            <a data-session-user-link data-relay-aware href="/login">Open profile</a>
          </p>
          <section class="login-recent-accounts-panel" data-recent-accounts-section hidden>
            <h3>Recent accounts</h3>
            <p class="muted">Stored signing accounts stay available on this browser so you can switch back without re-entering your <code>nsec</code>.</p>
            <ul class="login-recent-accounts-list" data-recent-accounts-list></ul>
          </section>
          <div class="login-tab-panel-actions" data-session-logout-wrap hidden>
            <button type="button" data-logout>Log out</button>
          </div>
        <div class="ascii-border">+------------------------------------------------------------------------------+</div>
      </section>
    </section>
  `;
}

function settingsMainMarkup() {
  return `
    <section class="feed-column shell-main-top" data-shell-main>
      <section class="page-heading">
        <h1>Settings</h1>
      </section>
      <section class="settings-card">
        <div class="settings-form settings-preferences">
          <h2>Modes</h2>
          <div class="settings-mode-tree-group">
            <div class="settings-mode-tree-group-main settings-mode-tree-group-main-stacked">
              <div class="settings-mode-tree" data-settings-mode-row>
                <div class="settings-mode-branch" aria-hidden="true"></div>
                <div class="settings-mode-copy">
                  <strong>Media Mode</strong>
                  <small class="muted">Replaces urls in notes with a collapsible media footer.</small>
                </div>
                <div class="settings-mode-switch" role="group" aria-label="Media Mode toggle">
                  <input id="image-mode-toggle" type="checkbox" data-image-mode-toggle>
                  <button type="button" class="settings-mode-option" data-image-mode-set="off">OFF</button>
                  <button type="button" class="settings-mode-option" data-image-mode-set="on">ON</button>
                </div>
              </div>
            </div>
          </div>
          <h2>Accounts</h2>
          <div class="settings-mode-tree-group" data-account-switcher-root>
            <div class="settings-mode-tree-group-main">
              <div class="settings-slider-tree settings-mode-tree-stacked">
                <div class="settings-mode-branch" aria-hidden="true"></div>
                <div class="settings-mode-row-head">
                  <strong>Recent signing accounts</strong>
                </div>
                <div class="settings-mode-row-body">
                  <small class="muted">Switch the active signer for replies and new notes in this browser. Stored signing accounts remain limited to the most recent three.</small>
                  <ul class="settings-account-list" data-account-switcher-list>
                    <li class="muted">No recent signing accounts yet.</li>
                  </ul>
                </div>
              </div>
            </div>
          </div>
          <section class="desktop-storage-settings" data-desktop-storage hidden>
            <h2>Local Storage</h2>
            <div class="settings-mode-tree-group">
              <div class="settings-mode-tree-group-main">
                <div class="settings-slider-tree settings-mode-tree-stacked">
                  <div class="settings-mode-branch" aria-hidden="true"></div>
                  <div class="settings-mode-row-head">
                    <strong>Storage Used</strong>
                    <span data-storage-total>Calculating...</span>
                  </div>
                  <div class="settings-mode-row-body desktop-storage-usage">
                    <span><span>SQLite cache</span><strong data-storage-sqlite>—</strong></span>
                    <span><span>WebView cache</span><strong data-storage-browser>—</strong></span>
                    <span><span>Note Data</span><strong data-storage-notes>—</strong></span>
                    <span><span>Metadata</span><strong data-storage-metadata>—</strong></span>
                    <span><span>User Data</span><strong data-storage-user-data>—</strong></span>
                  </div>
                </div>
                <div class="settings-slider-tree settings-mode-tree-stacked">
                  <div class="settings-mode-branch" aria-hidden="true"></div>
                  <div class="settings-mode-row-head">
                    <strong>Cache Controls</strong>
                  </div>
                  <div class="settings-mode-row-body">
                    <small class="muted">Desktop cache is unlimited and stays on this device. Clearing cache preserves accounts, private keys, sessions, and settings.</small>
                    <div class="desktop-storage-actions">
                      <button type="button" data-desktop-storage-refresh>Refresh Usage</button>
                      <button type="button" data-desktop-storage-clear="notes">Clear Note Data</button>
                      <button type="button" data-desktop-storage-clear="metadata">Clear Metadata</button>
                      <button type="button" data-desktop-storage-clear="user_data">Clear User Data</button>
                      <button type="button" class="danger" data-desktop-storage-clear="all">Clear All Cache</button>
                    </div>
                    <small class="muted" data-desktop-storage-status>Calculating storage usage...</small>
                  </div>
                </div>
              </div>
            </div>
          </section>
          <div class="settings-blossom-section" data-blossom-settings-section hidden>
            <h2>Blossom uploads</h2>
            <div class="settings-mode-tree-group" data-blossom-settings>
              <div class="settings-mode-tree-group-main">
                <div class="settings-slider-tree settings-mode-tree-stacked">
                  <div class="settings-mode-branch" aria-hidden="true"></div>
                  <div class="settings-mode-row-head"></div>
                  <div class="settings-mode-row-body">
                    <small class="muted">New posts upload images from your browser directly to a Blossom HTTPS endpoint (BUD-02). The first host is tried first; others are fallbacks. If uploads fail (often CORS), try another preset or your own host.</small>
                    <p class="settings-blossom-actions">
                      <button type="button" class="link-button" data-blossom-reset>Reset to defaults</button>
                    </p>
                  </div>
                </div>
                <div role="radiogroup" aria-labelledby="blossom-host-heading" class="settings-blossom-radiogroup">
                  <div class="settings-slider-tree settings-mode-tree-stacked settings-blossom-preset-row">
                    <div class="settings-mode-branch" aria-hidden="true"></div>
                    <div class="settings-mode-row-body">
                      <label class="settings-blossom-preset-card">
                        <input type="radio" name="blossom-preset" value="primal" data-blossom-preset="primal">
                        <span class="settings-blossom-preset-meta">
                          <strong>Primal</strong>
                          <small class="muted"><code>blossom.primal.net</code> first, then <code>blossom.nostr.build</code>.</small>
                        </span>
                      </label>
                    </div>
                  </div>
                  <div class="settings-slider-tree settings-mode-tree-stacked settings-blossom-preset-row">
                    <div class="settings-mode-branch" aria-hidden="true"></div>
                    <div class="settings-mode-row-body">
                      <label class="settings-blossom-preset-card">
                        <input type="radio" name="blossom-preset" value="nostr_build" data-blossom-preset="nostr_build">
                        <span class="settings-blossom-preset-meta">
                          <strong>nostr.build</strong>
                          <small class="muted"><code>blossom.nostr.build</code> first, then <code>blossom.primal.net</code>.</small>
                        </span>
                      </label>
                    </div>
                  </div>
                  <div class="settings-slider-tree settings-mode-tree-stacked settings-blossom-preset-row">
                    <div class="settings-mode-branch" aria-hidden="true"></div>
                    <div class="settings-mode-row-body">
                      <label class="settings-blossom-preset-card">
                        <input type="radio" name="blossom-preset" value="custom" data-blossom-preset="custom">
                        <span class="settings-blossom-preset-meta">
                          <strong>Custom base URL</strong>
                          <small class="muted">Your URL is tried first; built-in hosts remain as fallbacks.</small>
                        </span>
                      </label>
                    </div>
                  </div>
                </div>
                <div class="settings-slider-tree settings-mode-tree-stacked" data-blossom-custom-wrap>
                  <div class="settings-mode-branch" aria-hidden="true"></div>
                  <div class="settings-mode-row-head">
                    <strong>Custom Blossom URL</strong>
                  </div>
                  <div class="settings-mode-row-body">
                    <input id="blossom-custom-url" class="settings-blossom-custom-input" type="url" inputmode="url" autocomplete="off" placeholder="https://example.com/blossom/" data-blossom-custom-url aria-label="Custom Blossom base URL">
                  </div>
                </div>
              </div>
            </div>
          </div>
          <h2>Web of Trust</h2>
          <div class="settings-mode-tree-group">
            <div class="settings-mode-tree-group-main">
              <div class="settings-slider-tree settings-mode-tree-stacked" data-settings-slider-row>
                <div class="settings-mode-branch" aria-hidden="true"></div>
                <div class="settings-mode-row-head">
                  <strong>Feed Scope</strong>
                  <div class="settings-depth-control">
                    <select id="settings-wot-depth" class="feed-wot-depth-select settings-wot-depth-select" data-wot-depth aria-label="Web of Trust depth">
                      <option value="1">wot: 1°</option>
                      <option value="2">wot: 2°</option>
                      <option value="3">wot: 3°</option>
                    </select>
                  </div>
                </div>
                <div class="settings-mode-row-body">
                  <small class="muted">Feeds stay limited to a web of trust so relay reads remain bounded. Logged-out browsing uses Gigi's graph; signed-in browsing uses your follow list.</small>
                  <small class="muted" data-wot-eligibility-note hidden>Follow at least 1 user to build your own graph.</small>
                </div>
              </div>
            </div>
          </div>
          <h2>Relays</h2>
          ${relayManagerPanelsMarkup()}
        </div>
      </section>
    </section>
  `;
}

function supportMainMarkup() {
  return `
    <section class="feed-column shell-main-top" data-shell-main>
      <section class="page-heading legal-page">
        ${infoPageBrand()}
        <h1>Support</h1>
        <p class="legal-page-lead muted">Help for Plain Text Nostr on iOS and the web reader at <a href="https://plaintextnostr.com">plaintextnostr.com</a>.</p>

        <h2>Contact</h2>
        <p>Email: <a href="mailto:support@plaintextnostr.com">support@plaintextnostr.com</a></p>
        <p>Bug reports and feature requests: <a href="https://github.com/MCMXMCM/ptxttr/issues" rel="noopener noreferrer" target="_blank">GitHub issues</a></p>

        <h2>Getting started</h2>
        <ul>
          <li><strong>Browse without signing in.</strong> Launch the app or open the website to read notes from public Nostr relays using the default web-of-trust feed.</li>
          <li><strong>Sign in (optional).</strong> Use your Nostr key to publish notes, reactions, reposts, and bookmarks. Keys stay on your device.</li>
          <li><strong>Manage relays.</strong> Open Relays in the menu to view connections or add your own relay URLs.</li>
        </ul>

        <h2>Common questions</h2>
        <h3>What is Nostr?</h3>
        <p>Nostr is an open protocol for decentralized social networking. Notes are published to relays you choose, not to a single company server. Plain Text Nostr is a client that reads and publishes that public data.</p>

        <h3>Where are my keys stored?</h3>
        <p>On iOS, signing keys are stored in the device Keychain. On the web reader, keys are kept in your browser session. Plain Text Nostr does not operate a centralized account system and cannot recover a lost private key (<code>nsec</code>).</p>

        <h3>Who can see my posts?</h3>
        <p>Public Nostr notes are broadcast to relays and can be read by anyone using any compatible client. Direct messages use NIP-17/NIP-44 encryption between participants. Treat relay operators as third parties with their own policies.</p>

        <h3>Photo library access (iOS)</h3>
        <p>The app requests photo-library permission only when you tap Save on an image in a note. It does not scan or upload your photo library.</p>

        <h3>Reporting content</h3>
        <p>Plain Text Nostr displays user-generated content from third-party relays. You can mute accounts, adjust web-of-trust depth, or switch relays. For urgent safety issues, contact <a href="mailto:support@plaintextnostr.com">support@plaintextnostr.com</a>.</p>

        ${infoPageFooter()}
      </section>
    </section>
  `;
}

function marketingMainMarkup() {
  return `
    <section class="feed-column shell-main-top" data-shell-main>
      <section class="page-heading legal-page">
        ${infoPageBrand()}
        <h1>Plain Text Nostr for iOS</h1>
        <p class="legal-page-lead muted">A simple and fast Nostr web reader for iOS.</p>

        <h2>TestFlight beta</h2>
        <p><a href="https://testflight.apple.com/join/pz6ggn7D" rel="noopener noreferrer" target="_blank">Join the TestFlight beta</a> to install the latest builds on your iPhone or iPad.</p>

        <h2>Read the open social web</h2>
        <p>Plain Text Nostr connects to public Nostr relays and renders notes, threads, profiles, and long-form articles with a minimal interface. Your feed is stored locally in SQLite so you can keep scrolling through content you have already loaded, even when relays are slow or offline.</p>

        <h2>Features</h2>
        <ul>
          <li>Home feed with web-of-trust filtering</li>
          <li>Thread view with full conversation context</li>
          <li>Long-form Reads for kind-30023 articles</li>
          <li>Search, hashtags, and profile pages</li>
          <li>Bookmarks and notifications when signed in</li>
          <li>Relay management - defaults or your own relays</li>
          <li>Media mode for image grids and fullscreen viewing</li>
          <li>Save images from notes to your photo library</li>
        </ul>

        <h2>Optional sign-in</h2>
        <p>Browse logged out with a curated web-of-trust seed, or sign in with your Nostr key to publish notes, reactions, reposts, and bookmarks. Keys are stored on your device in the iOS Keychain.</p>

        <h2>Also on the web</h2>
        <p>The same Plain Text Nostr reader is available in your browser at <a href="/feed" data-relay-aware data-feed-home>plaintextnostr.com</a>. The iOS app is a native SwiftUI client; it is not affiliated with any single relay or Nostr service.</p>

        <h2>Legal</h2>
        <p>Review our <a href="/privacy" data-relay-aware data-main-menu-link>Privacy Policy</a> and <a href="/terms" data-relay-aware data-main-menu-link>Terms of Service</a>. Questions? Visit <a href="/support" data-relay-aware data-main-menu-link>Support</a>.</p>

        ${infoPageFooter()}
      </section>
    </section>
  `;
}

function termsMainMarkup() {
  return `
    <section class="feed-column shell-main-top" data-shell-main>
      <section class="page-heading legal-page">
        ${infoPageBrand()}
        <h1>Terms of Service</h1>
        <p class="legal-page-meta muted">Last updated: June 12, 2026</p>
        <p class="legal-page-lead muted">These Terms of Service ("Terms") govern your use of Plain Text Nostr, including the iOS application and the web reader at <a href="https://plaintextnostr.com">plaintextnostr.com</a> (together, the "Service"), operated by Matthew McCarty ("we", "us", or "our").</p>

        <h2>1. Acceptance</h2>
        <p>By accessing or using the Service, you agree to these Terms. If you do not agree, do not use the Service.</p>

        <h2>2. Description of the Service</h2>
        <p>Plain Text Nostr is a Nostr protocol client. It fetches public events from relays you configure, caches them locally for performance, and lets you publish signed events when you choose to sign in. The Service does not operate Nostr relays and does not control content on the Nostr network.</p>

        <h2>3. Eligibility</h2>
        <p>You must be at least 13 years old (or the minimum age required in your country) to use the Service. If you are under 18, you represent that you have permission from a parent or guardian.</p>

        <h2>4. Accounts and keys</h2>
        <ul>
          <li>Nostr identity is cryptographic. Your public key (<code>npub</code>) and private key (<code>nsec</code>) are created or imported by you.</li>
          <li>You are solely responsible for safeguarding your private key and any device used to sign events.</li>
          <li>We do not host centralized accounts and cannot reset, recover, or revoke lost keys.</li>
          <li>Signing in is optional for reading; publishing requires a key under your control.</li>
        </ul>

        <h2>5. User content and conduct</h2>
        <p>The Service displays user-generated content retrieved from third-party relays. You are responsible for content you publish and for complying with applicable law. You agree not to use the Service to:</p>
        <ul>
          <li>Violate any law or third-party rights</li>
          <li>Harass, threaten, or abuse others</li>
          <li>Distribute malware or attempt to disrupt relays or the Service</li>
          <li>Impersonate others or misrepresent your affiliation</li>
        </ul>
        <p>We do not pre-moderate Nostr network content. You may mute accounts, adjust filters, or stop using specific relays.</p>

        <h2>6. Third-party relays and services</h2>
        <p>Relays, media servers (including Blossom-compatible hosts), Lightning wallets, and linked websites are operated by third parties. Their availability, policies, and data handling are outside our control. Your use of those services is at your own risk and subject to their terms.</p>

        <h2>7. Intellectual property</h2>
        <p>The Plain Text Nostr application, website design, and branding are owned by us or our licensors. Nostr events and public protocol data remain the responsibility of their publishers. Open-source components are subject to their respective licenses.</p>

        <h2>8. Disclaimer of warranties</h2>
        <p>THE SERVICE IS PROVIDED "AS IS" AND "AS AVAILABLE" WITHOUT WARRANTIES OF ANY KIND, WHETHER EXPRESS OR IMPLIED, INCLUDING MERCHANTABILITY, FITNESS FOR A PARTICULAR PURPOSE, AND NON-INFRINGEMENT. WE DO NOT WARRANT THAT THE SERVICE WILL BE UNINTERRUPTED, ERROR-FREE, OR FREE OF HARMFUL CONTENT FROM THIRD-PARTY RELAYS.</p>

        <h2>9. Limitation of liability</h2>
        <p>TO THE MAXIMUM EXTENT PERMITTED BY LAW, WE WILL NOT BE LIABLE FOR ANY INDIRECT, INCIDENTAL, SPECIAL, CONSEQUENTIAL, OR PUNITIVE DAMAGES, OR ANY LOSS OF DATA, KEYS, PROFITS, OR GOODWILL, ARISING FROM YOUR USE OF THE SERVICE OR THIRD-PARTY RELAYS, EVEN IF WE HAVE BEEN ADVISED OF THE POSSIBILITY OF SUCH DAMAGES. OUR TOTAL LIABILITY FOR ANY CLAIM RELATING TO THE SERVICE WILL NOT EXCEED USD $100.</p>

        <h2>10. Indemnification</h2>
        <p>You agree to indemnify and hold us harmless from claims arising out of your use of the Service, your published content, or your violation of these Terms.</p>

        <h2>11. Changes</h2>
        <p>We may update these Terms from time to time. The "Last updated" date above will change when we do. Continued use after changes become effective constitutes acceptance of the revised Terms.</p>

        <h2>12. Termination</h2>
        <p>You may stop using the Service at any time. You may delete local data from the app or browser. Publishing a NIP-62 vanish request, when supported, is your mechanism to request removal of your events from relays that honor it. We may discontinue the Service at any time.</p>

        <h2>13. Governing law</h2>
        <p>These Terms are governed by the laws of the United States and the State of Delaware, without regard to conflict-of-law rules, except where mandatory local consumer protections apply.</p>

        <h2>14. Contact</h2>
        <p>Questions about these Terms: <a href="mailto:support@plaintextnostr.com">support@plaintextnostr.com</a> or <a href="/support" data-relay-aware data-main-menu-link>Support</a>.</p>

        ${infoPageFooter()}
      </section>
    </section>
  `;
}

function privacyMainMarkup() {
  return `
    <section class="feed-column shell-main-top" data-shell-main>
      <section class="page-heading legal-page">
        ${infoPageBrand()}
        <h1>Privacy Policy</h1>
        <p class="legal-page-meta muted">Last updated: June 12, 2026</p>
        <p class="legal-page-lead muted">This Privacy Policy describes how Plain Text Nostr ("we", "us", or "our") handles information when you use the iOS application and the web reader at <a href="https://plaintextnostr.com">plaintextnostr.com</a> (together, the "Service").</p>

        <h2>Summary</h2>
        <p>Plain Text Nostr is a decentralized social client. We do not operate a traditional account system, do not sell personal data, and do not use advertising trackers. Most data stays on your device or is fetched from public Nostr relays you choose.</p>

        <h2>Information we do not collect</h2>
        <ul>
          <li>We do not require your name, email address, or phone number to browse the Service.</li>
          <li>We do not run third-party advertising or cross-app tracking SDKs.</li>
          <li>We do not receive your private key (<code>nsec</code>) on our servers when you use the iOS app.</li>
        </ul>

        <h2>Information stored on your device</h2>
        <p>The Service caches Nostr events, profiles, and related metadata locally for speed and offline reading:</p>
        <ul>
          <li><strong>iOS:</strong> SQLite database on device; signing keys in the iOS Keychain when you sign in; app preferences in local storage.</li>
          <li><strong>Web:</strong> SQLite database on the server for the web reader's aggregated cache; browser session storage for keys and relay preferences during your session.</li>
        </ul>
        <p>You can clear cached data from in-app settings where available, or by removing the app.</p>

        <h2>Nostr network data</h2>
        <p>When you use the Service, it requests public events from Nostr relays. That data is defined by the Nostr protocol and is public by design (notes, reactions, profiles, relay lists, and similar event kinds). Publishing attaches your public key to events you sign. Relay operators may log connections, IP addresses, and subscription filters according to their own policies.</p>

        <h2>Signing and publishing</h2>
        <p>If you sign in, events are signed on your device (iOS) or in your browser (web) and sent to relays you select. We do not operate a centralized posting backend for the iOS app. The web reader may proxy relay connections from its server to improve performance.</p>

        <h2>Encrypted direct messages</h2>
        <p>When you use encrypted direct messages (NIP-17/NIP-44), ciphertext is exchanged via relays. Plain Text Nostr decrypts messages locally for display. We do not hold plaintext of your DMs on a central server.</p>

        <h2>Media and linked content</h2>
        <p>Notes may link to images, videos, or files hosted on third-party media servers (including Blossom-compatible hosts). Loading that media contacts those hosts directly or through optional proxies. Those hosts have their own privacy practices.</p>

        <h2>Photo library (iOS only)</h2>
        <p>If you tap Save on an image in a note, the app requests permission to add that image to your photo library. The app does not read or upload unrelated photos.</p>

        <h2>Support communications</h2>
        <p>If you email <a href="mailto:support@plaintextnostr.com">support@plaintextnostr.com</a> or open a GitHub issue, we receive the information you choose to send (such as your email address, device model, and description of the problem) solely to respond to your request.</p>

        <h2>Server logs (web reader)</h2>
        <p>The plaintextnostr.com web deployment may record standard web server logs (IP address, user agent, requested URL, timestamps) for security and operations. These logs are not used for advertising profiles.</p>

        <h2>Children</h2>
        <p>The Service is not directed to children under 13, and we do not knowingly collect personal information from children.</p>

        <h2>Your choices</h2>
        <p>You may browse without signing in, choose which relays to use, delete local data from your device, and stop using the Service at any time.</p>

        <h2>Changes</h2>
        <p>We may update this Privacy Policy from time to time. The "Last updated" date above indicates when the latest changes took effect.</p>

        <h2>Contact</h2>
        <p>Questions about privacy: <a href="mailto:support@plaintextnostr.com">support@plaintextnostr.com</a> or <a href="/support" data-relay-aware data-main-menu-link>Support</a>.</p>

        ${infoPageFooter()}
      </section>
    </section>
  `;
}

export function renderStaticStubMainContent(pathname) {
  switch (pathname) {
    case "/login":
      return loginMainMarkup();
    case "/about":
      return aboutMainMarkup();
    case "/relays":
      return `
        <section class="feed-column shell-center" data-shell-main>
          ${relayManagerMarkup()}
        </section>
      `;
    case "/settings":
      return settingsMainMarkup();
    case "/support":
      return supportMainMarkup();
    case "/ios-plain-text-nostr":
      return marketingMainMarkup();
    case "/terms":
      return termsMainMarkup();
    case "/privacy":
      return privacyMainMarkup();
    default:
      return "";
  }
}
