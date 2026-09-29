import { useEffect, useState } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { api } from '../api/client.js'
import { addToCart } from '../lib/cart.js'
import { Cover, money } from '../lib/books.jsx'

const SELLOS = ['ALMENA', 'TINTA VIVA', 'LOS PLIEGOS', 'CASA REDONDA', 'FONDO DEL SUR', 'EL ASTILLERO']

function BookCard({ book }) {
  const navigate = useNavigate()
  return (
    <article className="bcard" onClick={() => navigate(`/libro/${book.id}`)}>
      <div className="bcard-cover">
        <Cover book={book} />
        {book.stock === 0 && <span className="badge badge-out">Agotado</span>}
      </div>
      <h3 className="bcard-title">{book.title}</h3>
      <p className="bcard-author">{book.author}</p>
      <p className="bcard-price">
        <b>{money(book.price_cents)}</b>
      </p>
    </article>
  )
}

export default function Home() {
  const [books, setBooks] = useState([])
  const [featuredIdx, setFeaturedIdx] = useState(0)
  const navigate = useNavigate()

  useEffect(() => {
    api.get('/books').then((res) => setBooks(res.data || []))
  }, [])

  const withCover = books.filter((b) => b.url_cover_image)
  const featured = withCover.length > 0 ? withCover : books
  const hero = featured[featuredIdx % Math.max(1, featured.length)]

  useEffect(() => {
    if (featured.length < 2) return
    const timer = setInterval(() => setFeaturedIdx((i) => i + 1), 6000)
    return () => clearInterval(timer)
  }, [featured.length])

  const bestSellers = books.slice(0, 10)
  const magazineBook = books.find((b) => b.stock > 5) || books[0]

  return (
    <>
      {hero && (
        <section className="hero">
          <div className="hero-text">
            <p className="kicker">Selección de la semana</p>
            <h1 className="hero-title">{hero.title}</h1>
            <p className="hero-author">de {hero.author}</p>
            <p className="hero-excerpt">
              {hero.pages} páginas de prosa con oficio. Una edición cuidada para leer despacio y releer pronto.
            </p>
            <div className="hero-cta">
              <button className="btn btn-solid" onClick={() => navigate(`/libro/${hero.id}`)}>
                Leer más
              </button>
              <button
                className="btn btn-line"
                onClick={() => {
                  addToCart(hero.id)
                  navigate('/cart')
                }}
              >
                Añadir al carrito — {money(hero.price_cents)}
              </button>
            </div>
          </div>

          <div className="hero-figure">
            <div className="hero-blob" />
            <Cover book={hero} className="cover-hero" />
          </div>

          {featured.length > 1 && (
            <div className="hero-dots">
              {featured.slice(0, 4).map((b, i) => (
                <button
                  key={b.id}
                  aria-label={`destacado ${i + 1}`}
                  className={i === featuredIdx % 4 ? 'dot on' : 'dot'}
                  onClick={() => setFeaturedIdx(i)}
                />
              ))}
            </div>
          )}
        </section>
      )}

      <section className="sellos" aria-label="Sellos asociados">
        {SELLOS.map((s) => (
          <span key={s}>{s}</span>
        ))}
      </section>

      <section className="section">
        <header className="section-head">
          <h2>Más vendidos</h2>
          <Link to="/catalogo" className="section-link">
            Ver catálogo completo →
          </Link>
        </header>
        <div className="mosaic">
          {bestSellers.map((b, i) => (
            <div key={b.id} className={`mosaic-item ${i % 5 === 0 ? 'tall' : ''}`}>
              <BookCard book={b} />
            </div>
          ))}
        </div>
      </section>

      {magazineBook && (
        <section className="magazine">
          <div className="magazine-figure">
            <div className="magazine-shape" />
            <Cover book={magazineBook} className="cover-magazine" />
          </div>
          <div className="magazine-text">
            <p className="kicker">Del cuaderno del lector</p>
            <h2 className="magazine-title">{magTitle(magazineBook)}</h2>
            <blockquote className="magazine-quote">
              “Lo empecé una tarde cualquiera y lo terminé a las tres de la mañana. Hay libros que uno
              simplemente no puede dejar a mitad de capítulo.”
            </blockquote>
            <p className="magazine-byline">— Reseñas verificadas por la comunidad de lectores</p>
            <button className="btn btn-line" onClick={() => navigate(`/libro/${magazineBook.id}`)}>
              Ver el libro y sus reseñas
            </button>
          </div>
        </section>
      )}
    </>
  )
}

function magTitle(book) {
  return `${book.title}, de ${book.author}`
}