package main

import "fmt"

// Catálogo 100% FICTICIO: autores y títulos inventados para el lab.
// 10 autores × 20 títulos = 200 libros exactos.

type fictionalAuthor struct {
	name   string
	titles []string
}

var fictionalCatalog = []fictionalAuthor{
	{
		name: "Dorian Wells",
		titles: []string{
			"El Fuego de los Dioses Olvidados", "La Sangre del Minotauro", "El Oráculo de Neón",
			"Hijos del Olimpo Roto", "La Profecía del Relámpago Gris", "El Escudo que No Es Mío",
			"El Mapa de los Vientos", "La Corona del Inframundo", "Sirenas en el Metro",
			"El Desafío del Cíclope", "Las Cenizas de Delfos", "El Guardián del Laberinto",
			"Semidioses y Monstruos Urbanos", "La Espada de Verano", "El Juramento del Tridente",
			"Ninfa Blues", "El Último Augurio", "La Marca del Fénix", "Titanes Sobre Ruedas",
			"El Taller de Hefesto",
		},
	},
	{
		name: "Selena Marchetti",
		titles: []string{
			"Las Llaves de Estigia", "El Ojo de la Gorgona", "Vientos de Olimpo",
			"El Carro del Sol", "La Reina de las Amazonas", "Pergaminos del Egeo",
			"El Falso Oráculo", "Hijas de la Luna de Plata", "El Torneo de los Héroes",
			"La Fuente de la Juventud Miente", "Dragones del Etna", "El Cantar de la Espada de Bronce",
			"Los Perros del Hades", "El Regalo del Río Leteo", "Cazadores del Amanecer",
			"La Trampa del Dédalo", "El Susurro de las Arpías", "Coro de Victorias",
			"El Espejo de Afrodita", "La Última Sibila",
		},
	},
	{
		name: "Aria Blackwood",
		titles: []string{
			"El Distrito de los Silenciados", "Jaulas de Cristal", "La Elección de los Números",
			"Ciudad Sin Relojes", "Los Exiliados del Sector 7", "El Precio del Silencio",
			"Memoria Borrada", "La Revolución de los Invisibles", "Hijos del Último Amanecer",
			"El Sorteo de las Cenizas", "Protocolo Libertad", "La Muralla de Vidrio",
			"Susurros Prohibidos", "El Jardín de las Máquinas", "Generación Cero",
			"La Lista de los Marcados", "Fuego Bajo el Hielo", "El Concurso de los Elegidos",
			"Nadie Duerme en Nueva Terra", "La Caída del Gran Archivo",
		},
	},
	{
		name: "Cassia Reyne",
		titles: []string{
			"Órbita Cero", "Las Estrellas No Perdonan", "El Enjambre de Titán",
			"Corazones de Circuito", "La Colonia Perdida", "Señal Fantasma",
			"El Último Puente Estelar", "Gravedad Rota", "Los Hijos de Marte Rojo",
			"La Nave de los Cien Sueños", "Ecos del Vacío", "El Protocolo Andrómeda",
			"Rebelión en Luna Nueva", "La Chica que Viajaba al Ayer", "Códigos de Hielo",
			"El Mercado de los Soles", "Estación Fin del Mundo", "La Guerra de los Clones Silenciosos",
			"Pulso Estelar", "El Legado de la Cometa",
		},
	},
	{
		name: "Evangeline Rosseau",
		titles: []string{
			"La Dama y el Contrabandista", "Cartas desde el Atardecer", "El Baile de Medianoche",
			"Un Amor en la Costa de Normandía", "La Heredera Rebelde", "Susurros en el Invierno",
			"El Vizconde que Prometió", "Rosas para la Condesa", "La Promesa de Verano",
			"El Retrato Prohibido", "Bajo el Cielo de Provenza", "La Gobernanta y el Capitán",
			"Ternura en París", "El Último Vals del Duque", "Jardines de Sepia",
			"La Carta que Nunca Llegó", "Encaje y Pólvora", "El Silbido del Tren de las Cinco",
			"Amor en Tiempos de Tinta", "La Ventana de la Calle Luna",
		},
	},
	{
		name: "Clara Whitmore",
		titles: []string{
			"La Librería del Callejón", "El Café de las Tres Hermanas", "Segunda Oportunidad en Maple Street",
			"La Tejedora de Historias", "Otoño en Highland", "El Pan de la Abuela Emilia",
			"Cartas al Faro", "La Boda de la Aldea", "Un Verano en la Isla Menor",
			"El Jardín de los Recuerdos", "La Modista de Baker Row", "Corazón de Terciopelo",
			"El Tren de las Nueve", "La Casa de los Girasoles", "Nieve en Abril",
			"El Pianista del Barrio Viejo", "Mermelada y Nostalgia", "La Herencia de Willow Lane",
			"Bajo la Lluvia de Mayo", "El Relojero de San Telmo",
		},
	},
	{
		name: "Thalan Mireaux",
		titles: []string{
			"El Trono de Ceniza y Roble", "La Espada que Canta", "Reinos de Bruma",
			"El Pacto de los Nueve Clanes", "La Torre Sin Puerta", "Dragón de Sal",
			"El Camino del Cuervo Blanco", "Crónicas del Valle Roto", "La Reina de los Bosques Grises",
			"Fuego Frío", "El Herrero de Runas", "Las Tierras del Último Sol",
			"El Juramento de Piedra", "Hijos de la Tormenta Larga", "La Ciudad que Flotaba",
			"El Rey Bajo la Montaña", "Lágrimas de Acero", "El Bosque de los Nombres",
			"La Profecía del Río Negro", "Coronas de Escarcha",
		},
	},
	{
		name: "Nadia Okafor",
		titles: []string{
			"El Jardín de los Androides", "Mente Sintética", "La Deriva de los Años Luz",
			"Archivo de Almas", "El Silencio de Kepler", "Semillas de Titán",
			"Cielos Sintéticos", "El Último Humano de la Estación", "Redes de Neón",
			"La Biblioteca de Cristal", "Órbitas de Papel", "El Sueño de las Máquinas",
			"Frontera Índigo", "Los Jardines de Marte", "Pulsar",
			"El Eco de la Nave Madre", "Vidas en Silicio", "La Estación de los Pájaros de Hierro",
			"Cero Absoluto", "El Cartógrafo de Nebulosas",
		},
	},
	{
		name: "Vera Halloway",
		titles: []string{
			"La Casa que Miente", "Nadie Vio el Tren Pasar", "El Caso de la Viuda Gris",
			"Susurros en el Piso Nueve", "La Testigo de Madera", "Cerraduras y Mentiras",
			"El Hombre del Andén Tres", "Diario de una Noche Larga", "La Habitación 404",
			"Ceniza en la Alfombra", "El Jardín Cerrado", "Retrato de una Desaparecida",
			"La Última Llamada", "Voces Bajo el Hielo", "El Espejo del Séptimo Piso",
			"Cartas de un Muerto", "La Niña del Faro", "Mentiras de Medianoche",
			"El Pasajero Sin Nombre", "Tormenta en Puerto Sombra",
		},
	},
	{
		name: "Julián Ferrán",
		titles: []string{
			"El Verano en que Aprendimos a Nadar", "El Peso de las Palabras", "Vidas Pequeñas",
			"El Cartógrafo de Barrios", "Tardes con Mi Abuelo Rafael", "La Ciudad de los Gatos Dormidos",
			"Cafés que Ya No Existen", "El Año en que Todo Se Rompió", "Rutas de Autobús Nocturno",
			"La Chica del Mercado Central", "Manual para Despedirse", "El Ruido del Mar en las Cañerías",
			"Historias de la Calle Olmo", "Los Domingos del Panadero", "Nadar Hasta la Otra Orilla",
			"El Inventario de los Días", "Cartografía del Dolor Ajeno", "Pequeñas Traiciones",
			"El Faro del Fin del Paseo", "La Vida en Cuarentena",
		},
	},
}

// fictionalBook deriva todos los campos de forma determinista a partir del índice global.
type fictionalBook struct {
	author, title, isbn string
	pages, stock        int
	// priceCents is an integer number of cents, matching model.Book.PriceCents.
	// The field used to be a plain int named price feeding a NUMERIC(10,2)
	// column, so 599 was stored as 599.00 and a book priced at five ninety-nine
	// was displayed as 599.
	priceCents int64
}

func buildFictionalBooks() []fictionalBook {
	books := make([]fictionalBook, 0, 200)
	for ai, a := range fictionalCatalog {
		for ti, title := range a.titles {
			i := ai*20 + ti // 0..199
			books = append(books, fictionalBook{
				author:     a.name,
				title:      title,
				isbn:       fmt.Sprintf("978%07d", i+1), // 9780000001..9780000200 (10 chars)
				pages:      180 + (i*53)%470,
				priceCents: int64(599 + (i*137)%1400),
				stock:      5 + (i*7)%36,
			})
		}
	}
	return books
}
