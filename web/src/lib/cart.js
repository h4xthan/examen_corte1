// The cart is a list of what the visitor intends to buy, not a priced basket.
//
// Nothing here stores a price. The old version kept `price` on every line, and
// the totals in the interface were computed from it, so a cart that had been
// open since before a price change would go on showing, and offering to pay, the
// old figure. Every total is now computed from the catalogue the server sent,
// and the only thing the cart holds is identity and quantity.

const KEY = 'cart'
const EVENT = 'cart-changed'
// Quantity 1..10, the same range the server validates. Mirroring it here means
// the interface cannot offer a quantity that checkout will reject.
const MIN_QTY = 1
const MAX_QTY = 10

// normalise repairs a cart read from storage instead of trusting it.
//
// localStorage is the one input in this application that the user fully owns, so
// it can be edited by hand, left over from an older version, or simply be JSON
// that is not an array. The version that introduced quantities stored lines of
// {id, price} with no quantity at all, and a line like that sums to NaN the
// moment the page reads it.
//
// Everything that survives is coerced and range-checked, and anything that does
// not describe a line is dropped. A malformed cart is an inconvenience, not a
// reason to break the page.
export function normaliseCart(value) {
  if (!Array.isArray(value)) return []

  const seen = new Set()
  const out = []

  for (const raw of value) {
    if (!raw || typeof raw !== 'object') continue

    const id = toId(raw.id)
    if (id === null || seen.has(id)) continue

    // A missing quantity is the old format, where every line meant one copy.
    // Anything non-numeric or out of range is clamped rather than discarded, so
    // a cart written by a future version of the app degrades instead of
    // emptying itself.
    const quantity = clamp(toInt(raw.quantity) ?? 1)

    seen.add(id)
    out.push({ id, quantity })
  }

  return out
}

// toId keeps a book id as text and never as a number.
//
// Book ids are 19-digit integers that the server quotes on purpose: they are
// larger than 2^53, so the moment they become a JavaScript number the value is
// silently rounded and the cart ends up naming a book that does not exist. It is
// also what makes an old cart work: a line saved when ids were still unquoted
// is a number here, and String(number) hands back the digits it was stored
// with. A cart written before ids were quoted at all holds values that are
// already rounded, and no coercion can recover those, so the book is simply not
// found and the line is dropped. That is the only honest outcome: the alternative
// is sending a rounded id to checkout.
function toId(value) {
  if (typeof value === 'string' && value.trim() !== '') return value.trim()
  if (typeof value === 'number' && Number.isFinite(value) && Number.isInteger(value) && value > 0) {
    return String(value)
  }
  return null
}

function toInt(value) {
  if (typeof value === 'number') return Number.isFinite(value) ? Math.trunc(value) : null
  if (typeof value !== 'string' || value.trim() === '') return null
  const n = Number(value)
  return Number.isFinite(n) ? Math.trunc(n) : null
}

function clamp(n) {
  return Math.min(MAX_QTY, Math.max(MIN_QTY, n))
}

export function getCart() {
  try {
    return normaliseCart(JSON.parse(localStorage.getItem(KEY) || '[]'))
  } catch {
    return []
  }
}

export function saveCart(items) {
  // Round-tripping through normalise keeps the same repair on the way out, so a
  // caller that builds a line by hand cannot store something unreadable either.
  const clean = normaliseCart(items)
  localStorage.setItem(KEY, JSON.stringify(clean))
  window.dispatchEvent(new Event(EVENT))
}

// addToCart raises the quantity of a line that is already in the cart, up to the
// maximum, instead of pushing a second line for the same book. Two lines for one
// title are indistinguishable to the customer and confusing to the server,
// which reserves stock per line.
export function addToCart(bookID) {
  const id = toId(bookID)
  if (id === null) return
  const cart = getCart()
  const existing = cart.find((item) => item.id === id)
  if (existing) {
    existing.quantity = clamp(existing.quantity + 1)
  } else {
    cart.push({ id, quantity: 1 })
  }
  saveCart(cart)
}

export function removeFromCart(bookID) {
  const id = toId(bookID)
  if (id === null) return
  saveCart(getCart().filter((item) => item.id !== id))
}

export function setQuantity(bookID, quantity) {
  const id = toId(bookID)
  if (id === null) return
  const n = toInt(quantity) ?? 1
  if (n < MIN_QTY) {
    removeFromCart(id)
    return
  }
  saveCart(getCart().map((item) => (item.id === id ? { id, quantity: clamp(n) } : item)))
}

export function clearCart() {
  saveCart([])
}

export function cartCount(cart) {
  return cart.reduce((total, item) => total + item.quantity, 0)
}

export { MAX_QTY, MIN_QTY }
