+++
date = 2014-08-03T11:23:45Z
title = "hugo archetypes"
type = "post"
series = ["Hugo 101"]
draft = true
+++




This is a post in a series about Hugo:
<ul class="series">
  {{ _, $page := range .Site.Taxonomies.series.hugo101 }}
  {{if ne $page.Url .Url}}<li><a href="{{ $page.Url }}">{{ $page.Name }}</a></li>
  {{else}}<li>{{ $page.Name }}</li>
  {{end}}
  {{ end }}
</ul>