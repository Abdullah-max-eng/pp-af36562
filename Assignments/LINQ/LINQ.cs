using System;
using System.Collections.Generic;
using System.Linq;

class LinqListExample
{
    public static void Main()
    {
        List<Person> people = new List<Person>
        {
            new Person("Alice", 23),
            new Person("Bob", 17),
            new Person("Charlie", 30),
            new Person("Dana", 15)
        };

        // LINQ query: get names of adults age ≥ 18
        var adults =
            from p in people
            where p.Age >= 18
            select p.Name;

        Console.WriteLine("Adults:");
        foreach (var name in adults)
        {
            Console.WriteLine(name);
        }
    }

    class Person
    {
        public string Name;
        public int Age;

        public Person(string name, int age)
        {
            Name = name;
            Age = age;
        }
    }
}
