// plain(it) is the text of a content value, so the suite can compare what typst made of
// converted markup with the text the user wrote.
#let sequence = [*a* b].func()
#let plain(it) = {
  if it == none { "" }
  else if type(it) == str { it }
  else if it.func() == [ ].func() or it.func() == linebreak or it.func() == parbreak { " " }
  else if it.has("text") { plain(it.text) }
  else if it.has("children") { it.children.map(plain).join(if it.func() == sequence { "" } else { " " }, default: "") }
  else if it.has("body") { plain(it.body) }
  else { "" }
}
#let normalize(text) = text.replace(regex("\s+"), " ").trim()

// Every case's markup must evaluate to its expected text; a failing case panics with both.
#let data = json("/data.json")
#for case in data.cases {
  let content = eval(case.markup, mode: "markup")
  assert.eq(normalize(plain(content)), case.expected, message: case.name)
  content
  parbreak()
}
