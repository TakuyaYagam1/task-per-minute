const expected = [
  108, 96, 118, 127, 114, 119, 123, 119, 78, 119, 123,
  96, 78, 118, 114, 113, 99, 105, 116, 117, 125, 116,
];

function check(input) {
  const keys = [17, 18, 19, 20];
  const actual = [...input]
    .reverse()
    .map((character, index) => character.charCodeAt(0) ^ keys[index % keys.length]);

  return actual.length === expected.length &&
    actual.every((value, index) => value === expected[index]);
}

module.exports = { check };
