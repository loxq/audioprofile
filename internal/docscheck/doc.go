// Package docscheck ne contient que des tests : il vérifie que la documentation (README.md et docs/) ne ment pas.
//
//   - tous les liens relatifs pointent vers un fichier qui existe, et toutes les ancres vers un titre qui existe ;
//   - chaque bloc de code Go compile (programme complet ou fragment) ;
//   - chaque déclaration montrée (signature de fonction, type de réglages) est identique à celle du code ;
//   - les blocs YAML sont des configurations valides, ainsi que le fichier d'exemple.
//
// Un bloc qui ne doit pas être vérifié se marque d'une ligne <!-- doccheck:skip --> juste avant son ouverture.
package docscheck
