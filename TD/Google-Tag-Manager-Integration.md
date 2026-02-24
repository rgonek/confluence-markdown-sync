---
title: Google Tag Manager Integration
id: "4980920"
space: TD
version: 2
labels:
    - integrations
    - gtm
    - javascript
    - sdk-delivery
author: Robert Gonek
created_at: "2026-02-24T14:56:57Z"
last_modified_at: "2026-02-24T14:56:58Z"
last_modified_by: Robert Gonek
---
# Google Tag Manager Integration

If your team manages marketing tags through Google Tag Manager (GTM) and cannot deploy code changes to load the Luminary SDK directly, you can use a Custom HTML tag in GTM to load and initialize the SDK. This approach works for all GTM container types targeting web pages.

## Prerequisites

- An existing GTM container installed on your site.
- A Luminary workspace write key (found in **Settings → Workspace → API Keys → Write Keys**).
- GTM container publish access.

---

## Step 1: Create the Custom HTML Tag

1. In GTM, navigate to **Tags → New**.
2. Set the tag name to `Luminary Analytics - Init`.
3. Click **Tag Configuration** and select **Custom HTML**.
4. Paste the following into the HTML field:

```html
<script>
  (function() {
    // Load the Luminary UMD bundle asynchronously
    var script = document.createElement('script');
    script.src = 'https://cdn.luminaryapp.io/analytics/3/luminary.min.js';
    script.async = true;
    script.onload = function() {
      var writeKey = '{{Luminary Write Key}}'; // GTM variable
      var userId   = {{DL - User ID}};         // dataLayer variable, may be undefined
      var userEmail = {{DL - User Email}};

      var client = window.LuminaryAnalytics.createClient({
        writeKey: writeKey,
        debug: false,
      });

      // Store on window for other GTM tags to use
      window.luminary = client;

      // Identify if user context is available
      if (userId) {
        client.identify(String(userId), {
          email: userEmail || undefined,
        });
      }

      // Fire page view for the current page
      client.page();

      // Listen for future dataLayer pushes
      var originalPush = window.dataLayer.push.bind(window.dataLayer);
      window.dataLayer.push = function(event) {
        originalPush(event);
        if (event && event.event === 'luminary_track') {
          client.track(event.luminary_event_name, event.luminary_properties || {});
        }
        if (event && event.event === 'luminary_identify' && event.luminary_user_id) {
          client.identify(String(event.luminary_user_id), event.luminary_traits || {});
        }
      };
    };
    document.head.appendChild(script);
  })();
</script>
```

5. Under **Advanced Settings → Tag Sequencing**, check **Fire a tag before this tag fires** and select your cookie consent tag (if applicable) to ensure analytics only loads after consent is granted.

---

## Step 2: Create GTM Variables

Create the following GTM variables to inject workspace-specific values without hardcoding them.

`Luminary Write Key` — Constant variable:

- Type: Constant
- Value: your workspace write key (e.g., `wk_abc123`)

[Screenshot: GTM constant variable configuration showing Name = "Luminary Write Key" and Value field]

`DL - User ID` — Data Layer variable (for passing authenticated user ID from your application):

- Type: Data Layer Variable
- Data Layer Variable Name: `userId`
- Data Layer Version: Version 2

`DL - User Email` — Data Layer variable:

- Type: Data Layer Variable
- Data Layer Variable Name: `userEmail`
- Data Layer Version: Version 2

---

## Step 3: Configure the Trigger

Attach an **All Pages** trigger to fire the init tag on every page load.

[Screenshot: GTM trigger configuration showing trigger type "Page View - Window Loaded" with trigger firing condition "All Pages"]

Using **Window Loaded** rather than **DOM Ready** or **Page View** ensures the tag fires after your application's JavaScript has had a chance to push user context to the `dataLayer`.

---

## Step 4: Push User Context via dataLayer

In your application code, push user context to the `dataLayer` before GTM loads, or at any point after the user authenticates:

```javascript
// Push immediately if user is known on page load
window.dataLayer = window.dataLayer || [];
window.dataLayer.push({
  userId: '7f3a2c',
  userEmail: 'alice@example.com',
});

// After login/auth callback
window.dataLayer.push({
  event: 'luminary_identify',
  luminary_user_id: '7f3a2c',
  luminary_traits: {
    email: 'alice@example.com',
    plan: 'enterprise',
  },
});

// Track a custom event
window.dataLayer.push({
  event: 'luminary_track',
  luminary_event_name: 'Report Downloaded',
  luminary_properties: {
    report_id: 'rpt_q1_2025',
    format: 'pdf',
  },
});
```

---

## Step 5: Publish the Container

1. Click **Submit** in GTM.
2. Add a version name and description (e.g., `Add Luminary Analytics init tag`).
3. Click **Publish**.

Verify the integration is working by opening the browser console on your site and confirming there are no JavaScript errors, and that `window.luminary` is defined after page load. Enable debug mode temporarily by setting `debug: true` in the Custom HTML tag to see event logs in the console.

---

## Limitations

- The GTM-based installation uses the UMD bundle, which is larger (~32 kB gzipped) than the ESM build available via npm. For performance-sensitive applications, prefer the [direct npm installation](https://placeholder.invalid/page/sdk%2Fjavascript-sdk.md).
- `dataLayer`-based event tracking requires coordination between your engineering and marketing teams to ensure event names and properties are standardized. Consider publishing a shared event dictionary.
- GTM's asynchronous loading means events fired immediately on page load (before `window.onload`) may be missed. Push these events to the `dataLayer` and handle them in the `dataLayer.push` override shown above.
