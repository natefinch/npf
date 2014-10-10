// Copyright 2014 Canonical Ltd.
// Licensed under the LGPLv3, see LICENCE file for details.

package charmstore

import "encoding/json"

var esIndex map[string]interface{}
var esMapping map[string]interface{}

func init() {
	if err := json.Unmarshal(esIndexText, &esIndex); err != nil {
		panic(err)
	}
	if err := json.Unmarshal(esMappingText, &esMapping); err != nil {
		panic(err)
	}
}

var esIndexText = []byte(`
{
  "settings": {
    "number_of_shards": 1,
    "analysis": {
      "filter": {
        "n3_20grams_filter": {
          "type": "nGram",
          "min_gram": 3,
          "max_gram": 20
        }
      },
      "analyzer": {
        "n3_20grams": {
          "type": "custom",
          "tokenizer": "standard",
          "filter": [
            "lowercase",
            "n3_20grams_filter"
          ]
        }
      }
    }
  }
}
`)

var esMappingText = []byte(`
{
  "entity": {
    "dynamic": "false",
    "properties": {
      "URL": {
        "type": "string",
        "index": "not_analyzed",
        "index_options": "docs"
      },
      "BaseURL": {
        "type": "string",
        "index": "not_analyzed",
        "index_options": "docs"
      },
      "BlobHash": {
        "type": "string",
        "index": "not_analyzed",
        "omit_norms": true,
        "index_options": "docs"
      },
      "UploadTime": {
        "type": "date",
        "format": "dateOptionalTime"
      },
      "CharmMeta": {
        "dynamic": "false",
        "properties": {
          "Name": {
            "type": "multi_field",
            "fields": {
              "Name": {
                "type": "string",
                "index": "not_analyzed",
                "omit_norms": true,
                "index_options": "docs"
              },
              "ngrams": {
                "type": "string",
                "analyzer": "n3_20grams",
                "include_in_all": false
              }
            }
          },
          "Summary": {
            "type": "string"
          },
          "Description": {
            "type": "string"
          },
          "Provides": {
            "dynamic": "false",
            "properties": {
              "Name": {
                "type": "string",
                "index": "not_analyzed",
                "omit_norms": true,
                "index_options": "docs"
              },
              "Role": {
                "type": "string",
                "index": "not_analyzed",
                "omit_norms": true,
                "index_options": "docs"
              },
              "Interface": {
                "type": "string",
                "index": "not_analyzed",
                "omit_norms": true,
                "index_options": "docs"
              },
              "Scope": {
                "type": "string",
                "index": "not_analyzed",
                "omit_norms": true,
                "index_options": "docs"
              }
            }
          },
          "Requires": {
            "dynamic": "false",
            "properties": {
              "Name": {
                "type": "string",
                "index": "not_analyzed",
                "omit_norms": true,
                "index_options": "docs"
              },
              "Role": {
                "type": "string",
                "index": "not_analyzed",
                "omit_norms": true,
                "index_options": "docs"
              },
              "Interface": {
                "type": "string",
                "index": "not_analyzed",
                "omit_norms": true,
                "index_options": "docs"
              },
              "Scope": {
                "type": "string",
                "index": "not_analyzed",
                "omit_norms": true,
                "index_options": "docs"
              }
            }
          },
          "Peers": {
            "dynamic": "false",
            "properties": {
              "Name": {
                "type": "string",
                "index": "not_analyzed",
                "omit_norms": true,
                "index_options": "docs"
              },
              "Role": {
                "type": "string",
                "index": "not_analyzed",
                "omit_norms": true,
                "index_options": "docs"
              },
              "Interface": {
                "type": "string",
                "index": "not_analyzed",
                "omit_norms": true,
                "index_options": "docs"
              },
              "Scope": {
                "type": "string",
                "index": "not_analyzed",
                "omit_norms": true,
                "index_options": "docs"
              }
            }
          },
          "Categories": {
            "type": "string",
            "index": "not_analyzed",
            "omit_norms": true,
            "index_options": "docs"
          }
        }
      },
      "CharmConfig": {
        "dynamic": "false",
        "properties": {
          "Options": {
            "dynamic": "false",
            "properties": {
              "Description": {
                "type": "string"
              },
              "Default": {
                "type": "string"
              }
            }
          }
        }
      },
      "CharmActions": {
        "dynamic": "false",
        "properties": {
          "description": {
            "type": "string"
          },
          "action_name": {
            "type": "string",
            "index": "not_analyzed",
            "omit_norms": true,
            "index_options": "docs"
          }
        }
      },
      "CharmProvidedInterfaces": {
        "type": "string",
        "index": "not_analyzed",
        "omit_norms": true,
        "index_options": "docs"
      },
      "CharmRequiredInterfaces": {
        "type": "string",
        "index": "not_analyzed",
        "omit_norms": true,
        "index_options": "docs"
      },
      "BundleData": {
        "type": "object",
        "dynamic": "false",
        "properties": {
          "Services": {
            "type": "object",
            "dynamic": "false",
            "properties": {
              "Charm": {
                "type": "string",
                "index": "not_analyzed",
                "omit_norms": true,
                "index_options": "docs"
              },
              "NumUnits": {
                "type": "integer",
                "index": "not_analyzed"
              }
            }
          },
          "Series": {
            "type": "string"
          },
          "Relations": {
            "type": "string",
            "index": "not_analyzed"
          }
        }
      },
      "BundleReadMe": {
        "type": "string",
        "index": "not_analyzed",
        "omit_norms": true,
        "index_options": "docs"
      },
      "BundleCharms": {
        "type": "string",
        "index": "not_analyzed",
        "omit_norms": true,
        "index_options": "docs"
      },
      "BundleMachineCount": {
        "type": "integer"
      },
      "BundleUnitCount": {
        "type": "integer"
      }
    }
  }
}
`)
