import { apiPath } from '../api/client.js'

export function money(cents) {
  return `$${(cents / 100).toFixed(2)}`
}

export function coverSrc(book) {
  return book.url_cover_image ? apiPath(book.url_cover_image) : ''
}

// There is no hasOffer/finalPrice any more.
//
// Both used to compute a 10% markdown in the browser from the book's id, and the
// catalogue and the detail page would render that number as the price. The server
// had never heard of it: checkout charged the catalogue price, so the site
// advertised a discount it did not apply and the customer's cart total was
// wrong before the request was even sent. A price is a fact about the shop, so it
// is read from the API and shown as it arrives.

const PUBLISHERS = ['Almena', 'Tinta Viva', 'Los Pliegos', 'Casa Redonda', 'Fondo del Sur', 'El Astillero']

export function publisherFor(book) {
  return PUBLISHERS[(book.id * 7) % PUBLISHERS.length]
}

export function yearFor(book) {
  return 1968 + ((book.id * 13) % 57)
}

export function ratingFor(reviews) {
  if (!reviews || reviews.length === 0) return null
  const sum = reviews.reduce((acc, r) => acc + r.rating, 0)
  return sum / reviews.length
}

export function Stars({ value }) {
  const full = Math.max(0, Math.min(5, Math.round(value)))
  return (
    <span className="stars" aria-label={`${value} de 5`}>
      {'★★★★★'.slice(0, full)}
      <span className="stars-off">{'★★★★★'.slice(0, 5 - full)}</span>
    </span>
  )
}

export function Cover({ book, className = '' }) {
  const initials = (book.title || '?')
    .split(' ')
    .map((w) => w[0])
    .slice(0, 2)
    .join('')
    .toUpperCase()
  return (
    <div className={`cover ${className}`}>
      {book.url_cover_image ? (
        <img src={coverSrc(book)} alt={book.title} loading="lazy" />
      ) : (
        <div className="cover-fallback">
          <span>{initials}</span>
        </div>
      )}
    </div>
  )
}

export function synopsisFor(book) {
  return (
    `${book.title} es una obra de ${book.author} que ha encontrado en las librerías ` +
    `un lugar entre los lectores que buscan prosa con oficio y tramas que no subestiman su inteligencia. ` +
    `A lo largo de ${book.pages} páginas, el libro alterna registros —el retrato íntimo, la escena precisa— ` +
    `sin perder nunca el pulso narrativo. Una edición cuidada para leer despacio y releer pronto.`
  )
}
