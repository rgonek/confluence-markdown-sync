---
title: Technical documentation
confluence_page_id: "1376436"
confluence_space_key: TD
confluence_version: 1
confluence_last_modified: "2026-02-19T19:47:57Z"
---
## Description

> [!CUSTOM]
> In a sentence or two, describe the purpose of this space.

## Search this Space

```adf:extension
{
  "type": "inlineExtension",
  "attrs": {
    "extensionKey": "livesearch",
    "extensionType": "com.atlassian.confluence.macro.core",
    "localId": "e72dc22f-8448-4c46-9126-0f5027bbf994",
    "parameters": {
      "macroMetadata": {
        "macroId": {
          "value": "1b95c6e7619bfe759b8aaadeb5178f9f"
        },
        "placeholder": [
          {
            "data": {
              "url": "https://rgonek.atlassian.net/wiki/download/resources/confluence.extra.livesearch/images/search.png"
            },
            "type": "icon"
          }
        ],
        "schemaVersion": {
          "value": "1"
        },
        "title": "Live Search"
      },
      "macroParams": {
        "spaceKey": {
          "value": "currentSpace()"
        }
      }
    }
  }
}
```



## Filter by Label

> [!INFO]
> These are all the labels in use in this space. Select a label to see a list of content the label has been applied to.

```adf:extension
{
  "type": "extension",
  "attrs": {
    "extensionKey": "listlabels",
    "extensionType": "com.atlassian.confluence.macro.core",
    "layout": "default",
    "localId": "8a2fdfbf-a690-4657-a257-e87109a1d1da",
    "parameters": {
      "macroMetadata": {
        "macroId": {
          "value": "851322ba5ee38a64dddfee0d9529c725"
        },
        "schemaVersion": {
          "value": "1"
        },
        "title": "Labels List"
      },
      "macroParams": {}
    }
  }
}
```

## Recently updated content

> [!INFO]
> This list below will automatically update each time somebody in your space creates or updates content.

```adf:extension
{
  "type": "extension",
  "attrs": {
    "extensionKey": "recently-updated",
    "extensionType": "com.atlassian.confluence.macro.core",
    "layout": "default",
    "localId": "c3e6e4fd-4b85-4e59-8dcd-57d4e0d217c8",
    "parameters": {
      "macroMetadata": {
        "macroId": {
          "value": "723adb2b644b9f38809318cd0a11d868"
        },
        "schemaVersion": {
          "value": "1"
        },
        "title": "Recent updates"
      },
      "macroParams": {
        "hideHeading": {
          "value": "true"
        },
        "max": {
          "value": "10"
        },
        "theme": {
          "value": "concise"
        },
        "types": {
          "value": "page,whiteboard,database,blog"
        }
      }
    }
  }
}
```
